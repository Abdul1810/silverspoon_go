package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"golang.org/x/net/websocket"
)

// cdpConn is a minimal Chrome DevTools Protocol client over the browser's
// websocket, using flattened sessions (sessionId on each message), the same
// low-level approach nodriver takes.
type cdpConn struct {
	ws      *websocket.Conn
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpMessage
	subs    map[int]func(method, session string, params json.RawMessage)
	subID   int
	done    chan struct{}
	err     error
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

var errClosed = errors.New("browser connection closed")

func dialCDP(wsURL string) (*cdpConn, error) {
	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	if err != nil {
		return nil, err
	}
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		return nil, err
	}
	ws.MaxPayloadBytes = 256 << 20
	c := &cdpConn{ws: ws, pending: map[int64]chan cdpMessage{},
		subs: map[int]func(string, string, json.RawMessage){}, done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *cdpConn) readLoop() {
	defer close(c.done)
	for {
		var raw []byte
		if err := websocket.Message.Receive(c.ws, &raw); err != nil {
			c.mu.Lock()
			c.err = err
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var m cdpMessage
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		if m.Method != "" {
			c.mu.Lock()
			subs := make([]func(string, string, json.RawMessage), 0, len(c.subs))
			for _, f := range c.subs {
				subs = append(subs, f)
			}
			c.mu.Unlock()
			for _, f := range subs {
				f(m.Method, m.SessionID, m.Params)
			}
		}
	}
}

// subscribe registers an event handler; the returned func removes it.
// Handlers run on the read goroutine and must not block or call back in.
func (c *cdpConn) subscribe(f func(method, session string, params json.RawMessage)) func() {
	c.mu.Lock()
	c.subID++
	id := c.subID
	c.subs[id] = f
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
	}
}

// call sends method to session ("" = browser) and decodes the result into out.
func (c *cdpConn) call(ctx context.Context, session, method string, params any, out any) error {
	id := c.nextID.Add(1)
	var p json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		p = b
	}
	msg, _ := json.Marshal(cdpMessage{ID: id, Method: method, SessionID: session, Params: p})
	ch := make(chan cdpMessage, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return errClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if err := websocket.Message.Send(c.ws, string(msg)); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case m, ok := <-ch:
		if !ok {
			return errClosed
		}
		if m.Error != nil {
			return fmt.Errorf("%s: %s (%d)", method, m.Error.Message, m.Error.Code)
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	}
}

func (c *cdpConn) close() {
	c.ws.Close()
	<-c.done
}

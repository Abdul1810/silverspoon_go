package ui

import (
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

func mustIcon(data []byte) *widget.Icon {
	ic, err := widget.NewIcon(data)
	if err != nil {
		panic(err)
	}
	return ic
}

// Icon set (Material Design).
var (
	icAll       = mustIcon(icons.ActionViewList)
	icDownload  = mustIcon(icons.FileFileDownload)
	icQueue     = mustIcon(icons.ActionSchedule)
	icDone      = mustIcon(icons.ActionCheckCircle)
	icError     = mustIcon(icons.AlertErrorOutline)
	icFolder    = mustIcon(icons.FileFolder)
	icFolderOp  = mustIcon(icons.FileFolderOpen)
	icAdd       = mustIcon(icons.ContentAdd)
	icSearch    = mustIcon(icons.ActionSearch)
	icSettings  = mustIcon(icons.ActionSettings)
	icAlarm     = mustIcon(icons.ActionAlarm)
	icDark      = mustIcon(icons.ImageBrightness2)
	icLight     = mustIcon(icons.ImageWBSunny)
	icHelp      = mustIcon(icons.ActionHelpOutline)
	icPlay      = mustIcon(icons.AVPlayArrow)
	icPause     = mustIcon(icons.AVPause)
	icStop      = mustIcon(icons.AVStop)
	icRetry     = mustIcon(icons.NavigationRefresh)
	icDelete    = mustIcon(icons.ActionDelete)
	icMore      = mustIcon(icons.NavigationMoreHoriz)
	icClose     = mustIcon(icons.NavigationClose)
	icExpand    = mustIcon(icons.NavigationExpandMore)
	icCollapse  = mustIcon(icons.NavigationChevronRight)
	icUnarchive = mustIcon(icons.ContentUnarchive)
	icRedo      = mustIcon(icons.AVReplay)
	icCopy      = mustIcon(icons.ContentContentCopy)
	icPaste     = mustIcon(icons.ContentContentPaste)
	icFile      = mustIcon(icons.EditorInsertDriveFile)
	icSpeed     = mustIcon(icons.ActionTrendingUp)
	icInfo      = mustIcon(icons.ActionInfoOutline)
	icImport    = mustIcon(icons.FileFileUpload)
	icClear     = mustIcon(icons.CommunicationClearAll)

	icEthernet = mustIcon(icons.ActionSettingsEthernet)
	icWiFi     = mustIcon(icons.DeviceSignalWiFi4Bar)
	icTether   = mustIcon(icons.DeviceWiFiTethering)
	icCellular = mustIcon(icons.DeviceSignalCellular4Bar)
	icNetOther = mustIcon(icons.HardwareRouter)
	icNetOff   = mustIcon(icons.DeviceSignalWiFiOff)
)

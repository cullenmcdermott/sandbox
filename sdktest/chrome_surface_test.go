package sdktest

// chrome_surface_test.go — compile-time pins for the public tui/chrome package:
// the reusable status line, context/token gauge, working indicator, and calm
// notices an external host uses to frame the transcript. Names no internal/ type.

import (
	"time"

	"github.com/cullenmcdermott/sandbox/tui/chrome"
)

var (
	// Status line.
	_ func(string) chrome.Segment         = chrome.Seg
	_ func(string) chrome.Segment         = chrome.Req
	_ func(int, ...chrome.Segment) string = chrome.StatusLine
	_                                     = chrome.Segment{Text: "", Required: false}

	// Context / token gauge.
	_ func(int, int) string     = chrome.ContextGauge
	_ func(float64, int) string = chrome.BlockBar

	// Working indicator.
	_ func(chrome.Working) string = chrome.WorkingIndicator
	_                             = chrome.Working{Verb: "", Elapsed: time.Second, OutputTokens: 0, Hint: "", Frame: 0}

	// Calm notices.
	_ func(chrome.NoticeKind, string, int) string = chrome.Notice
	_ chrome.NoticeKind                           = chrome.NoticeInfo
	_ chrome.NoticeKind                           = chrome.NoticeWarn
	_ chrome.NoticeKind                           = chrome.NoticeError
)

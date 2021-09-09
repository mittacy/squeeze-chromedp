package squeeze_chromedp

import (
	"github.com/chromedp/chromedp"
)

func initWindow() chromedp.Tasks {
	return chromedp.Tasks{
		chromedp.Navigate("about:blank"),
	}
}

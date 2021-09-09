package squeeze_chromedp

import (
	"fmt"
	"github.com/chromedp/chromedp"
	"io/ioutil"
	"log"
	"sync"
	"testing"
	"time"
)

var (
	wg sync.WaitGroup
)

func TestPooleSequential(t *testing.T) {
	c := Pool{
		InitActive:      1,           // 初始化连接数
		MaxIdle:         10,          // 最大空闲连接数
		MaxActive:       20,          // 最大连接数
		IdleTimeout:     time.Minute, // 空闲时间
		Wait:            false,       // 是否阻塞等待
		MaxConnLifetime: time.Hour,   // 连接生命周期
		ShowWindow:      false,       // 窗口可视化
	}

	pool := NewPool(&c)
	defer pool.Close()

	screen(pool, "https://www.baidu.com")

	screen(pool, "https://golang.org/")
}

func TestPooleConcurrent(t *testing.T) {
	c := Pool{
		MaxIdle:         10,          // 最大空闲连接数
		MaxActive:       20,          // 最大连接数
		IdleTimeout:     time.Minute, // 空闲时间
		Wait:            false,       // 是否阻塞等待
		MaxConnLifetime: time.Hour,   // 连接生命周期
	}

	pool := NewPool(&c)
	defer pool.Close()

	wg = sync.WaitGroup{}
	wg.Add(10)

	urls := [5]string{
		"https://golang.org/",
		"https://www.baidu.com",
		"http://blog.mittacy.com/",
		"https://segmentfault.com/",
		"https://tophub.today/",
	}

	for _, v := range urls {
		go screenWg(pool, v)
	}

	time.Sleep(time.Second * 30)

	for _, v := range urls {
		go screenWg(pool, v)
	}

	wg.Wait()
}

func screenWg(pool *Pool, url string) {
	defer wg.Done()

	screen(pool, url)
}

func screen(pool *Pool, url string) {
	conn := pool.Get()
	defer conn.Close()

	var buf []byte

	if err := chromedp.Run(conn.Get(), fullScreenshot(url, 100, &buf)); err != nil {
		log.Fatal(err)
		return
	}

	filename := fmt.Sprintf("./%d.png", time.Now().UnixNano())
	if err := ioutil.WriteFile(filename, buf, 0o644); err != nil {
		log.Fatal(err)
	}

	log.Println("pool stats: ", pool.Stats())
}

// fullScreenshot takes a screenshot of the entire browser viewport.
//
// Note: chromedp.FullScreenshot overrides the device's emulation settings. Reset
func fullScreenshot(urlstr string, quality int, res *[]byte) chromedp.Tasks {
	return chromedp.Tasks{
		chromedp.Navigate(urlstr),
		chromedp.FullScreenshot(res, quality),
	}
}

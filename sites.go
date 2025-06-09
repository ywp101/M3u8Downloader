package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// FetchVideoMeta
// metaName -> <meta name={metaName} content="..."/>
func FetchVideoMeta(videoURL string, metaName string, fn func(ctx context.Context, shtml *string) []chromedp.Action) *VideoMeta {
	fmt.Printf("Open Chromedp, Fetching video metadata for URL: %s\n", videoURL)

	// Step 1: 创建 chromedp 无头浏览器上下文
	ctx, cancel := createContextWithUA()
	defer cancel()

	// Step 2: 打开页面并执行 JS 提取 m3u8 链接
	var m3u8URL, shtml, title string
	var commonTitle string
	var commonTitleEval = "document.title"
	m3u8URLCh := make(chan string, 1)

	_ = chromedp.Run(ctx,
		network.Enable(),
		target.SetAutoAttach(true, false),
		target.SetDiscoverTargets(true),
	)

	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if ev, ok := ev.(*network.EventRequestWillBeSent); ok {
			targetURL := ev.Request.URL
			if strings.Contains(targetURL, ".m3u8") && m3u8URL == "" {
				m3u8URLCh <- targetURL
			}
		}
	})
	err := chromedp.Run(ctx,
		chromedp.Navigate(videoURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(10*time.Second), // 等待 JS 加载
		chromedp.OuterHTML(`html`, &shtml),
		chromedp.Evaluate(commonTitleEval, &commonTitle),
		//chromedp.Evaluate(m3u8Eval, &m3u8URL),
	)
	if fn != nil {
		actions := make([]chromedp.Action, 0)
		actions = append(actions, fn(ctx, &shtml)...)
		err = chromedp.Run(ctx, actions...)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Printf("Failed to fetch video metadata for URL %s: %v\n", videoURL, err)
		//return nil
	}
	reg, _ := regexp.Compile(`<meta[^>]+name=["']` + metaName + `["'][^>]+content=["']([^"']+)["']`)
	matches := reg.FindStringSubmatch(shtml)
	if len(matches) > 0 {
		title = matches[1]
	}

	if title == "" {
		title = commonTitle
	}

	select {
	case m3u8URL = <-m3u8URLCh:
	case <-time.After(30 * time.Second):
		fmt.Println("⚠️ 超时未捕获 m3u8")
		cancel()
	}
	//if m3u8URL == "" {
	//	reg, _ := regexp.Compile(`https:\/\/[^\s'"]+\.m3u8[^\s'"]*`)
	//	matches := reg.FindStringSubmatch(shtml)
	//	if len(matches) > 0 {
	//		m3u8URL = matches[1]
	//	}
	//}
	fmt.Printf("Fetched metadata - Title: %s, M3U8 URL: %s\n", title, m3u8URL)
	return &VideoMeta{
		URL:     videoURL,
		VideoID: hash(videoURL),
		Title:   title,
		M3u8URL: m3u8URL,
	}
}

func NormalFetchVideoMeta(videoURL string, metaName string) *VideoMeta {
	return FetchVideoMeta(videoURL, metaName, nil)
}

func FetchJableTVVideoMeta(videoURL string) *VideoMeta {
	return NormalFetchVideoMeta(videoURL, "og:title")
}

func FetchHohojTVVideoMeta(videoURL string) *VideoMeta {
	// iframe
	// todo: hohoj.tv可以改为http请求
	u, _ := url.Parse(videoURL)
	videoID := u.Query()["id"]
	embedVideoURL := fmt.Sprintf("https://hohoj.tv/embed?id=%s", videoID[0])
	//return NormalFetchVideoMeta(embedVideoURL, "description", "videoSrc")
	return NormalFetchVideoMeta(embedVideoURL, "description")
}

func FetchMissavAiVideoMeta(videoURL string) *VideoMeta {
	// 二级m3u8文件 playlist.m3u8 -> master.m3u8
	//return NormalFetchVideoMeta(videoURL, "twitter:title", "hls.url")
	return NormalFetchVideoMeta(videoURL, "twitter:title")
}

func FetchMemojavVideoMeta(videoURL string) *VideoMeta {
	// get_video_info.php找到m3u8链接
	// init.mp4 + .m4s
	// eg. https://memojav.com/hls/get_video_info.php?id=DLDSS-414&sig=MjU2OTE0Nw&sts=6287002
	return NormalFetchVideoMeta(videoURL, "twitter:title")
}

func FetchAVTodayIOVideoMeta(videoURL string) *VideoMeta {
	return NormalFetchVideoMeta(videoURL, "description")
}

func FetchNetflAVVideoMeta(_ string) *VideoMeta {
	// 而且很慢
	log.Fatal("NetflAV not support(use iframe)! use raw m3u8")
	return nil
	//return NormalFetchVideoMeta(videoURL, "description")
}

func FetchBzraizyVideoMeta(videoURL string) *VideoMeta {
	return NormalFetchVideoMeta(videoURL, "og:title")
}

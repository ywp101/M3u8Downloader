package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: m3u8downloader <video_page_url or filepath>")
		return
	}

	runtime.GOMAXPROCS(runtime.NumCPU())

	master := NewDLMaster()
	master.RegisterVideoHandle("https://jable.tv/", FetchJableTVVideoMeta)
	master.RegisterVideoHandle("https://hohoj.tv/", FetchHohojTVVideoMeta)
	master.RegisterVideoHandle("https://missav.ai/", FetchMissavAiVideoMeta)
	master.RegisterVideoHandle("https://memojav.com/", FetchMemojavVideoMeta)
	master.RegisterVideoHandle("https://avtoday.io/", FetchAVTodayIOVideoMeta)
	master.RegisterVideoHandle("https://netflav.com/", FetchNetflAVVideoMeta)
	master.RegisterVideoHandle("https://f15.bzraizy.cc/", FetchBzraizyVideoMeta)

	videoURL := os.Args[1]
	if strings.HasPrefix(videoURL, "http") {
		master.videoURLs = append(master.videoURLs, videoURL)
	} else {
		videoURLs := loadURLs(videoURL)
		master.videoURLs = videoURLs
	}

	master.Run()
}

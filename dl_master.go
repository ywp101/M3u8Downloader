package main

import (
	"log"
	"net/url"
	"strings"
)

type fetchVideoMetaFunc func(videoURL string) *VideoMeta

type VideoMeta struct {
	URL     string
	VideoID string
	Title   string
	M3u8URL string
}

type DLMaster struct {
	videoURLs   []string
	videoCh     chan VideoMeta
	videoHandle map[string]fetchVideoMetaFunc
}

func NewDLMaster() *DLMaster {
	return &DLMaster{
		videoCh:     make(chan VideoMeta, 3),
		videoHandle: make(map[string]fetchVideoMetaFunc),
	}
}

func (dm *DLMaster) RegisterVideoHandle(siteURL string, fn fetchVideoMetaFunc) {
	u, err := url.Parse(siteURL)
	if err != nil {
		log.Fatalf("Invalid URL: %s", siteURL)
	}
	dm.videoHandle[u.Host] = fn
}

func (dm *DLMaster) Run() {
	for i, vURL := range dm.videoURLs {
		log.Printf("Processing %d/%d: %s\n", i+1, len(dm.videoURLs), vURL)
		videoMeta := dm.FetchVideoMeta(vURL)
		if videoMeta == nil || videoMeta.M3u8URL == "" {
			log.Printf("Failed to fetch video metadata for URL: %s\n", vURL)
			continue
		}

		bakM3u8URLCh := make(chan string, 1)
		quitCh := make(chan bool, 1)
		dl := NewM3u8Downloader(videoMeta, "", bakM3u8URLCh)

		// todo: 是否有必要吗？
		//go func(bakM3u8URLCh chan string, quitCh chan bool, meta *VideoMeta) {
		//	for {
		//		select {
		//		case <-time.After(3 * time.Minute):
		//			//todo: nil
		//			videoMeta2 := dm.FetchVideoMeta(meta.URL)
		//			if videoMeta2.M3u8URL != meta.M3u8URL {
		//				bakM3u8URLCh <- videoMeta2.M3u8URL
		//				return
		//			}
		//		case <-quitCh:
		//			return
		//		}
		//	}
		//}(bakM3u8URLCh, quitCh, videoMeta)

		if err := dl.Download(); err != nil {
			log.Printf("Failed to download url[%s]: %v\n", vURL, err)
		}
		quitCh <- true
	}
}

func (dm *DLMaster) FetchVideoMeta(videoURL string) *VideoMeta {
	u, err := url.Parse(videoURL)
	if err != nil {
		log.Printf("Invalid URL: %s", videoURL)
		return nil
	}
	fetchFunc, exists := dm.videoHandle[u.Host]
	if !exists {
		log.Printf("No handler registered for host: %s, use default handler", u.Host)
		return dm.FetchDefaultVideoMeta(videoURL)
	}
	return fetchFunc(videoURL)
}

func (dm *DLMaster) FetchDefaultVideoMeta(m3u8URL string) *VideoMeta {
	if strings.Contains(m3u8URL, ".m3u8") {
		tmp := strings.SplitN(m3u8URL, ";", 2)
		if len(tmp) != 2 {
			log.Fatalf("Invalid m3u8 format, should be m3u8_url;title")
		}
		return &VideoMeta{
			URL:     tmp[0],
			VideoID: hash(tmp[0]),
			Title:   tmp[1],
			M3u8URL: tmp[0],
		}
	}
	return nil
}

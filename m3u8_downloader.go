package main

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"github.com/twmb/murmur3"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/levigross/grequests"
)

const (
	// HEAD_TIMEOUT 请求头超时时间
	HEAD_TIMEOUT = 10 * time.Second
)

type TsInfo struct {
	FileIndex   int
	URLPrefix   string
	URLLastName string // ts文件路径
}

func (ts TsInfo) URL() string {
	return fmt.Sprintf("%s/%s", ts.URLPrefix, ts.URLLastName)
}

type M3u8FileInfo struct {
	Host       string
	TsKey      string
	TsList     []TsInfo
	FailTsList []TsInfo
}

func (mf *M3u8FileInfo) FetchM3u8Content(m3u8URL string, ro *grequests.RequestOptions) *grequests.Response {
	r, err := grequests.Get(m3u8URL, ro)
	if err != nil {
		log.Panic(err)
	}
	return r
}

func (mf *M3u8FileInfo) parseM3u8TsKey(data string) error {
	reg, _ := regexp.Compile(`#EXT-X-KEY.*URI="(.*?)"`)
	tsKeyURLs := reg.FindStringSubmatch(data)
	if len(tsKeyURLs) == 0 {
		return fmt.Errorf("no ts key found in m3u8 content")
	}
	keyURL := tsKeyURLs[1]
	if !strings.HasPrefix(keyURL, "http") {
		// 如果没有http前缀，则拼接host
		keyURL = fmt.Sprintf("%s/%s", mf.Host, keyURL)
	}
	res, err := grequests.Get(keyURL, NewHttpOptions(""))
	if err != nil {
		return err
	}

	if res.StatusCode != 200 {
		return fmt.Errorf("failed to fetch ts key from %s, status code: %d", keyURL, res.StatusCode)
	}
	mf.TsKey = res.String()
	return nil
}

func (mf *M3u8FileInfo) ParseM3u8Content(m3u8URL string, ro *grequests.RequestOptions) error {
	mf.Host = getHost(m3u8URL, "v1")
	data := mf.FetchM3u8Content(m3u8URL, ro)
	scanner := bufio.NewScanner(data)
	i := 0
	extInf, streamInf := false, false
	streams := make([]string, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 多码率
		if streamInf {
			if strings.HasPrefix(line, "http") {
				streams = append(streams, line)
			} else {
				streams = append(streams, fmt.Sprintf("%s/%s", mf.Host, line))
			}
			streamInf = false
		} else if extInf {
			// 兼容ts文件，存在ts/jpg/jpeg/m4s的情况
			i++
			ts := TsInfo{FileIndex: i, URLLastName: line, URLPrefix: mf.Host}
			// ts 列表
			if strings.HasPrefix(line, "http") {
				ts.URLPrefix = getHost(line, "v1")
				ts.URLLastName = filepath.Base(line)
			}
			mf.TsList = append(mf.TsList, ts)
			extInf = false
		} else if strings.HasPrefix(line, "#EXT-X-KEY") && strings.Contains(line, "URI") {
			// m3u8 key
			err := mf.parseM3u8TsKey(line)
			if err != nil {
				return err
			}
		} else if strings.HasPrefix(line, "#EXTINF:") {
			extInf = true
		} else if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			streamInf = true
		} else if strings.HasPrefix(line, "#EXT-X-MAP") && strings.Contains(line, "URI") {
			// support m4s格式
			//#EXT-X-MAP:URI="init.mp4"
			i++
			reg, _ := regexp.Compile(`#EXT-X-MAP.*URI="(.*?)"`)
			tmp := reg.FindStringSubmatch(line)
			if len(tmp) == 0 {
				return fmt.Errorf("no init.mp4 found in m3u8 content")
			}
			mf.TsList = append(mf.TsList, TsInfo{FileIndex: i, URLLastName: tmp[1], URLPrefix: mf.Host})
		}
	}
	if len(streams) != 0 {
		return mf.ParseM3u8Content(streams[len(streams)-1], ro)
	}
	return nil
}

type M3u8Downloader struct {
	videoID      string
	OutputPath   string // 输出路径
	tmpPath      string
	videoMeta    *VideoMeta    // 视频元数据
	m3u8Meta1    *M3u8FileInfo // m3u8文件信息
	m3u8Meta2    *M3u8FileInfo // m3u8文件信息
	bakM3u8URLCh chan string
	doFailMu     sync.Mutex                // 处理失败的任务锁
	ro           *grequests.RequestOptions // 请求选项
	tsWriter     *TsWriter
}

func NewM3u8Downloader(videoMeta *VideoMeta, outputPath string, bakM3u8URLCh chan string) *M3u8Downloader {
	tmpPath := fmt.Sprintf("%s%s", os.TempDir(), videoMeta.VideoID)
	if _, err := os.Stat(tmpPath); os.IsNotExist(err) {
		if err := os.MkdirAll(tmpPath, 0755); err != nil {
			log.Fatalf("Failed to create temporary directory: %v", err)
		}
	}
	log.Println("Temporary directory created:", tmpPath)
	tsWriter := NewTsWriter(tmpPath)
	return &M3u8Downloader{
		videoMeta:    videoMeta,
		OutputPath:   outputPath,
		tmpPath:      tmpPath,
		m3u8Meta1:    &M3u8FileInfo{},
		m3u8Meta2:    &M3u8FileInfo{},
		bakM3u8URLCh: bakM3u8URLCh,
		doFailMu:     sync.Mutex{},
		ro:           NewHttpOptions(videoMeta.M3u8URL),
		tsWriter:     tsWriter,
	}
}

func (md *M3u8Downloader) Download() error {
	mvName := filepath.Join(md.OutputPath, md.videoMeta.Title+".mp4")
	if _, err := os.Stat(mvName); err == nil {
		log.Printf("[info] Video file %s already exists, skipping download\n", mvName)
		return nil
	}

	err := md.m3u8Meta1.ParseM3u8Content(md.videoMeta.M3u8URL, md.ro)
	if err != nil {
		return err
	}
	go func() {
		select {
		case m3u8URL := <-md.bakM3u8URLCh:
			log.Printf("[info] Received backup m3u8 URL: %s\n", m3u8URL)
			_ = md.m3u8Meta2.ParseM3u8Content(m3u8URL, md.ro)
		}
	}()

	md.tsWriter.StartMerge()
	ConcurrencyRun(md, 24)
	//	todo: 输出下载视频信息
	return nil
}

func (md *M3u8Downloader) DoDispatch() (<-chan MRTask, <-chan int) {
	totalCh := make(chan int, 1)
	outCh := make(chan MRTask, 128)

	go func() {
		total := 0
		for _, ts := range md.m3u8Meta1.TsList {
			if !md.tsWriter.CheckTsIsExist(ts.FileIndex) {
				total++
			}
		}
		totalCh <- total
		close(totalCh)
		log.Printf("[info] Dispatch %d tasks for downloading ts files\n", total)

		// todo：动态调整速率
		for _, ts := range md.m3u8Meta1.TsList {
			if md.tsWriter.CheckTsIsExist(ts.FileIndex) {
				continue
			}
			outCh <- NewMRTask(ts, 5, "")
		}

		md.doFailMu.Lock()
		defer md.doFailMu.Unlock()
		log.Printf("[info] Dispatched %d tasks for retrying failed ts files, secondary m3u8 key = %s \n", len(md.m3u8Meta1.FailTsList), md.m3u8Meta2.TsKey)
		if len(md.m3u8Meta1.FailTsList) == 0 || md.m3u8Meta2.TsKey == "" {
			close(outCh)
			return
		}
		for _, ts := range md.m3u8Meta1.FailTsList {
			failTask := NewMRTask(ts, 5, "")
			outCh <- *md.tryResetMRTask(&failTask)
		}
		close(outCh)
	}()

	return outCh, totalCh
}

func (md *M3u8Downloader) DoMap(in MRTask) ([]interface{}, error) {
	tsKey := md.m3u8Meta1.TsKey
	if in.extra != "" {
		tsKey = in.extra
	}
	ts := in.data.(TsInfo)
	err := md.downloadTs(ts, tsKey)
	return nil, err
}

func (md *M3u8Downloader) tryResetMRTask(in *MRTask) *MRTask {
	ts := in.data.(TsInfo)
	if ts.URLPrefix == md.m3u8Meta1.Host {
		ts.URLPrefix = md.m3u8Meta2.Host
		in.maxRetryCnt = 5
		in.extra = md.m3u8Meta2.TsKey
		return in
	} else {
		// 两个cdn地址都找不到文件，放弃
		return nil
	}
}

func (md *M3u8Downloader) DoFail(in MRTask) *MRTask {
	md.doFailMu.Lock()
	defer md.doFailMu.Unlock()

	if md.m3u8Meta2.TsKey == "" {
		md.m3u8Meta1.FailTsList = append(md.m3u8Meta1.FailTsList, in.data.(TsInfo))
		return nil
	}

	return md.tryResetMRTask(&in)
}

func (md *M3u8Downloader) DoReduce(_ []interface{}) interface{} {
	mergeFilePath := md.tsWriter.Flush()
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		// .ts -> .mp4
		// Merge: 多个 .ts 简单拼接（cat / io.Copy 合并，并不保证有合法的头部 / PAT/PMT 表
		// 播放会存在卡顿问题，同时部分播放器无法播放。
		_ = os.Rename(mergeFilePath, filepath.Join(md.OutputPath, md.videoMeta.Title+".mp4"))
		log.Println("[warn] ffmpeg not found, using simple merge method")
		return nil
	}
	log.Println("[info] ffmpeg found, using ffmpeg merge method")
	md.FFmpegMerge(mergeFilePath)
	return nil
}

func (md *M3u8Downloader) FFmpegMerge(mergeFile string) {
	// todo: 参考https://github.com/orestonce/m3u8d/blob/main/merge.go去修改
	baseName := filepath.Join(md.OutputPath, md.videoMeta.Title)
	cmd := exec.Command("ffmpeg", "-i", mergeFile, "-c", "copy", baseName+".mp4")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
	_ = os.Remove(mergeFile)
}

func (md *M3u8Downloader) downloadTs(ts TsInfo, tsKey string) error {
	defer func() {
		if r := recover(); r != nil {
			log.Println("[error] Panic occurred while downloading ts file:", ts, "Error:", r)
			//fmt.Println("网络不稳定，正在进行断点持续下载")
		}
	}()

	res, err := grequests.Get(ts.URL(), md.ro)
	if err != nil || !res.Ok {
		// todo: res.ok == false, need find why
		log.Println("[error] Failed to download ts file:", ts, "Error:", err)
		return retryError
	}
	var origData []byte
	origData = res.Bytes()
	contentLen := 0
	contentLenStr := res.Header.Get("Content-Length")
	if contentLenStr != "" {
		contentLen, _ = strconv.Atoi(contentLenStr)
	}
	if len(origData) == 0 || (contentLen > 0 && len(origData) < contentLen) || res.Error != nil {
		log.Println("[error] Incomplete ts file or error occurred:", ts, "Error:", res.Error)
		return retryError
	}
	if tsKey != "" {
		//解密 ts 文件，算法：aes 128 cbc pack5
		origData, err = AesDecrypt(origData, []byte(tsKey))
		if err != nil {
			return retryError
		}
		// https://en.wikipedia.org/wiki/MPEG_transport_stream
		// Some TS files do not start with SyncByte 0x47, they can not be played after merging,
		// Need to remove the bytes before the SyncByte 0x47(71).
		syncByte := uint8(71) //0x47
		bLen := len(origData)
		for j := 0; j < bLen; j++ {
			if origData[j] == syncByte {
				origData = origData[j:]
				break
			}
		}
	}

	md.tsWriter.WriteTs(ts.FileIndex, origData)
	return nil
}

func NewHttpOptions(m3u8Url string) *grequests.RequestOptions {
	ro := &grequests.RequestOptions{
		UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_13_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.88 Safari/537.36",
		RequestTimeout: HEAD_TIMEOUT,
		Headers: map[string]string{
			"Connection":      "keep-alive",
			"Accept":          "*/*",
			"Accept-Encoding": "*",
			"Accept-Language": "zh-CN,zh;q=0.9, en;q=0.8, de;q=0.7, *;q=0.5",
		},
	}
	ro.Headers["Referer"] = getHost(m3u8Url, "v2")
	return ro
}

// 获取m3u8地址的host
func getHost(Url, ht string) (host string) {
	u, err := url.Parse(Url)
	if err != nil {
		log.Panic(err)
	}
	switch ht {
	case "v1":
		host = u.Scheme + "://" + u.Host + filepath.Dir(u.EscapedPath())
	case "v2":
		host = u.Scheme + "://" + u.Host
	}
	return
}

// ============================== 加解密相关 ==============================

func PKCS7Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

func PKCS7UnPadding(origData []byte) []byte {
	length := len(origData)
	unpadding := int(origData[length-1])
	return origData[:(length - unpadding)]
}

func AesEncrypt(origData, key []byte, ivs ...[]byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	var iv []byte
	if len(ivs) == 0 {
		iv = key
	} else {
		iv = ivs[0]
	}
	origData = PKCS7Padding(origData, blockSize)
	blockMode := cipher.NewCBCEncrypter(block, iv[:blockSize])
	crypted := make([]byte, len(origData))
	blockMode.CryptBlocks(crypted, origData)
	return crypted, nil
}

func AesDecrypt(crypted, key []byte, ivs ...[]byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	var iv []byte
	if len(ivs) == 0 {
		iv = key
	} else {
		iv = ivs[0]
	}
	blockMode := cipher.NewCBCDecrypter(block, iv[:blockSize])
	origData := make([]byte, len(crypted))
	blockMode.CryptBlocks(origData, crypted)
	origData = PKCS7UnPadding(origData)
	return origData, nil
}

func hash(s string) string {
	hasher := murmur3.New32()
	_, _ = hasher.Write([]byte(s))
	return fmt.Sprintf("%x", hasher.Sum32())
}

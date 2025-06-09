package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/chromedp/chromedp"
	"os"
	"strings"
)

func createContextWithUA() (context.Context, context.CancelFunc) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserAgent(`Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36 Edg/137.0.0.0`),
		chromedp.Flag("disable-features", "site-per-process,Translate,BlinkGenPropertyTrees,IsolateOrigins,site-per-process"),
	)

	allocCtx, _ := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	return ctx, cancel
}

func loadURLs(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var result []string
	urls := make(map[string]bool)
	for scanner.Scan() {
		url := scanner.Text()
		if strings.HasPrefix(url, "http") && urls[url] == false {
			urls[url] = true
			result = append(result, url)
		} else {
			fmt.Printf("Skipping invalid URL: %s\n", url)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading file:", err)
		return nil
	}
	return result
}

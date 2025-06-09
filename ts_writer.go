package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type TsData struct {
	Index int
	Data  []byte
}

type MergeRange struct {
	// [Start, End)
	Start int
	End   int
}

type TsWriter struct {
	baseDir      string
	segments     []MergeRange
	buffer       map[int][]byte
	downloadChan chan TsData
	quitCh       chan struct{}
}

func NewTsWriter(tsDir string) *TsWriter {
	segments := make([]MergeRange, 0)
	files, _ := filepath.Glob(filepath.Join(tsDir, "*_*.ts"))
	for _, file := range files {
		var start, end int
		n, err := fmt.Sscanf(filepath.Base(file), "%d_%d.ts", &start, &end)
		if err != nil || n != 2 {
			continue
		}
		segments = append(segments, MergeRange{
			Start: start,
			End:   end,
		})
	}
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].Start < segments[j].Start
	})

	return &TsWriter{
		baseDir:      tsDir,
		buffer:       make(map[int][]byte),
		segments:     segments,
		downloadChan: make(chan TsData, 64),
		quitCh:       make(chan struct{}),
	}
}

func (tw *TsWriter) StartMerge() {
	const maxBufferSize = 40 * 1024 * 1024 // 40MB

	var (
		totalSize  int
		mergeTimer = time.NewTicker(10 * time.Second)
		lastTime   = time.Now()
	)

	go func() {
		for {
			select {
			case ts := <-tw.downloadChan:
				tw.buffer[ts.Index] = ts.Data
				totalSize += len(ts.Data)

				if totalSize >= maxBufferSize && len(tw.buffer) > 1 {
					tw.mergeBufferedTS(&tw.buffer, true)
					totalSize = 0
					lastTime = time.Now()
				}

			case <-mergeTimer.C:
				if len(tw.buffer) > 1 && time.Since(lastTime) > 30*time.Second {
					tw.mergeBufferedTS(&tw.buffer, false)
					totalSize = 0
					lastTime = time.Now()
				}
			case <-tw.quitCh:
				return
			}
		}
	}()
}

func (tw *TsWriter) mergeBufferedTS(buffer *map[int][]byte, skipSmallFile bool) {
	oldSize := len(*buffer)
	if oldSize == 0 {
		return
	}
	// 找连续段
	indexes := make([]int, 0, len(*buffer))
	for idx := range *buffer {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	start := indexes[0]
	end := start + 1
	for i := 1; i < len(indexes); i++ {
		if indexes[i] == end {
			end++
			continue
		}

		tw.writeBufferToFile(start, end, buffer, skipSmallFile)

		start = indexes[i]
		end = start + 1
	}
	// 写入最后一段 [start, end)
	tw.writeBufferToFile(start, end, buffer, skipSmallFile)
	newSize := len(*buffer)
	log.Printf("Merging TS %d files (from %d to %d)\n", oldSize-newSize, oldSize, newSize)
}

func (tw *TsWriter) writeBufferToFile(start, end int, buffer *map[int][]byte, skipSmallFile bool) {
	// skip, because too small
	if skipSmallFile && end-start < 10 {
		return
	}
	filePath := filepath.Join(tw.baseDir, fmt.Sprintf("%d_%d.ts", start, end))
	f, _ := os.Create(filePath)
	for i := start; i < end; i++ {
		data, _ := (*buffer)[i]
		_, _ = f.Write(data)
		delete(*buffer, i)
	}
	_ = f.Close()

	// 查找插入位置，保持 tw.segments 有序
	insertIndex := sort.Search(len(tw.segments), func(i int) bool {
		return tw.segments[i].Start >= start
	})
	tw.segments = append(tw.segments[:insertIndex], append([]MergeRange{{Start: start, End: end}}, tw.segments[insertIndex:]...)...)
}

func (tw *TsWriter) CheckTsIsExist(tsIndex int) bool {
	_, ok := sort.Find(len(tw.segments), func(i int) int {
		if tw.segments[i].End <= tsIndex {
			return 1
		}
		if tw.segments[i].Start > tsIndex {
			return -1
		}
		return 0
	})
	return ok
}

func (tw *TsWriter) WriteTs(tsIndex int, data []byte) {
	tw.downloadChan <- TsData{Index: tsIndex, Data: data}
}

func (tw *TsWriter) Flush() string {
	tw.quitCh <- struct{}{}
	tw.mergeBufferedTS(&tw.buffer, false)

	mergeFilePath := filepath.Join(tw.baseDir, "merge.ts")
	outMv, _ := os.Create(mergeFilePath)
	defer outMv.Close()
	writer := bufio.NewWriter(outMv)
	for _, seg := range tw.segments {
		in, _ := os.Open(fmt.Sprintf("%s/%d_%d.ts", tw.baseDir, seg.Start, seg.End))
		_, _ = io.Copy(writer, in)
		_ = in.Close()
	}
	_ = writer.Flush()
	return mergeFilePath
}

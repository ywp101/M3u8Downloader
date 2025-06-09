package main

import (
	"errors"
	"github.com/schollz/progressbar/v3"
	"log"
	"sync"
	"time"
)

/*
 DoDispatch -> DoMap -[all done]-> DoReduce
                \-[fail]-> DoMap(Retry) -[fail] -> DoFail
*/

type MapReduce interface {
	DoDispatch() (<-chan MRTask, <-chan int)
	DoMap(in MRTask) ([]interface{}, error)
	DoReduce(in []interface{}) interface{}
	DoFail(in MRTask) *MRTask
}

var retryError = errors.New("retry Task")

type MRTask struct {
	maxRetryCnt int
	extra       string
	data        interface{}
}

func NewMRTask(data interface{}, maxRetryCnt int, extra string) MRTask {
	return MRTask{
		maxRetryCnt: maxRetryCnt,
		data:        data,
		extra:       extra,
	}
}

func ConcurrencyRun(mr MapReduce, workCnt int) interface{} {
	outCh := make(chan interface{}, 128)
	doneCh := make(chan struct{}, 1)
	retryCh := make(chan MRTask, 128)
	wg, wgTask := &sync.WaitGroup{}, &sync.WaitGroup{}

	pb := progressbar.New(-1)
	now := time.Now()
	// 分配任务
	inCh, outTotal := mr.DoDispatch()

	// start worker
	for i := 0; i < workCnt; i++ {
		wg.Add(1)
		go func() {
			handleFn := func(in MRTask) {
				defer wgTask.Done()
				outs, err := mr.DoMap(in)
				if err != nil {
					if errors.Is(err, retryError) && in.maxRetryCnt > 0 {
						in.maxRetryCnt--
						wgTask.Add(1)
						retryCh <- in // 重新放回任务队列
						return
					}
					newMRTask := mr.DoFail(in) // 处理失败任务
					if newMRTask != nil {
						wgTask.Add(1)
						retryCh <- *newMRTask // 重新放回任务队列
					}
					return
				}
				_ = pb.Add(1)
				for _, out := range outs {
					outCh <- out
				}
			}
			defer wg.Done()
			for {
				select {
				case in, ok := <-inCh:
					if !ok {
						inCh = nil
						continue
					}
					handleFn(in)
				case in, ok := <-retryCh:
					if !ok {
						retryCh = nil
						continue
					}
					handleFn(in)
				case <-doneCh:
					return
				}
			}
		}()
	}

	// 收集所有 output
	var results []interface{}
	go func() {
		select {
		case t := <-outTotal:
			pb.ChangeMax(t)
			wgTask.Add(t)
		}
		wgTask.Wait()
		close(retryCh)
		close(doneCh)
		wg.Wait()
		close(outCh)
	}()

	// 收集阶段不需要额外线程，因为 mapper.Map 中不修改 wg，我们不加 wg，这里直接 range
	for out := range outCh {
		results = append(results, out)
	}

	// 最终归约结果
	reduceResult := mr.DoReduce(results)
	log.Println()
	log.Printf("🎯 All tasks completed in %s\n", time.Since(now))
	return reduceResult
}

package fsearch

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type FileSearch struct {
	mmap        []byte
	totalLen    int64
	lastOffset  int
	fileSize    int64
	fileName    string
	closeChan   chan struct{}
	rwMutex     sync.RWMutex
	updateTime  time.Time
	lineOffsets []int
	closeOnce   sync.Once
}

func (t *FileSearch) Close() {
	t.closeOnce.Do(func() {
		err := syscall.Munmap(t.mmap)
		if err != nil {
			log.Println(err)
		}
		close(t.closeChan)
	})
}
func (t *FileSearch) reset() {
	t.rwMutex.Lock()
	defer t.rwMutex.Unlock()
	t.lineOffsets = make([]int, 0, 1<<20)
	t.lastOffset = 0
	t.fileSize = 0
}
func NewFileSearch(fileName string) (*FileSearch, error) {
	f, err := os.Open(fileName)
	if err != nil {
		panic(err)
	}
	mmap, err := syscall.Mmap(int(f.Fd()), 0, 2<<30, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	err = f.Close()
	if err != nil {
		return nil, err
	}
	const MMapTotalLen = 2 << 30
	const preLines = 1 << 20
	t := &FileSearch{
		mmap:        mmap,
		totalLen:    MMapTotalLen,
		fileSize:    0,
		lastOffset:  0,
		rwMutex:     sync.RWMutex{},
		fileName:    fileName,
		lineOffsets: make([]int, 0, preLines),
		closeChan:   make(chan struct{}),
		closeOnce:   sync.Once{},
	}
	go t.detectFileSize()
	return t, nil
}
func (t *FileSearch) SearchFromStart(maxLines int, kws ...string) []string {
	t.rwMutex.RLock()
	defer t.rwMutex.RUnlock()
	var result []string
	kwsBytes := make([][]byte, 0, len(kws))
	for _, kw := range kws {
		kwsBytes = append(kwsBytes, []byte(kw))
	}

	lineStart := 0
	for _, offset := range t.lineOffsets {
		if int64(lineStart) >= t.fileSize {
			return result
		}
		if len(result) >= maxLines {
			return result
		}
		line := t.mmap[lineStart:offset]
		var in = true
		for _, kw := range kwsBytes {
			if !bytes.Contains(line, kw) {
				in = false
			}
		}
		if in {
			result = append(result, string(line))
		}
		lineStart = offset + 1

	}
	if int64(lineStart) < t.fileSize {
		line := t.mmap[lineStart:t.fileSize]
		fmt.Println("LAST", line)
		var in = true
		for _, kw := range kwsBytes {
			if !bytes.Contains(line, kw) {
				in = false
			}
		}
		if in {
			result = append(result, string(line))
		}
	}
	return result
}
func (t *FileSearch) SearchFromEnd(maxLines int, kws ...string) []string {
	t.rwMutex.RLock()
	defer t.rwMutex.RUnlock()
	var result []string
	defer func() {
		reverseArray(result)
	}()
	kwsBytes := make([][]byte, 0, len(kws))
	for _, kw := range kws {
		kwsBytes = append(kwsBytes, []byte(kw))
	}
	lineEnd := int(t.fileSize)
	for i := len(t.lineOffsets) - 1; i >= 0; i-- {
		offset := t.lineOffsets[i]
		if int64(lineEnd) <= 0 {
			return result
		}
		if len(result) >= maxLines {
			return result
		}
		line := t.mmap[offset+1 : lineEnd]
		var in = true
		for _, kw := range kwsBytes {
			if !bytes.Contains(line, kw) {
				in = false
			}
		}
		if in {
			result = append(result, string(line))
		}
		lineEnd = offset
	}
	return result
}
func (t *FileSearch) detectFileSize() {
	var retry = 0
	log.Println("fsearch: start detectFileSize", t.fileName)
	for {
		select {
		case <-t.closeChan:
			return
		default:
		}
		fileInfo, err := os.Stat(t.fileName)
		if err != nil {
			log.Println(err)
			if retry > 5 {
				return
			}
			time.Sleep(time.Second * 10)
			retry++
		}
		retry = 0
		size := fileInfo.Size()
		if int64(t.lastOffset) > size {
			t.reset()
			time.Sleep(time.Second * 10)
			continue
		}
		subs := t.mmap[t.lastOffset:size]
		t.rwMutex.Lock()
		for i := 0; i < len(subs); i++ {
			if subs[i] == '\n' {
				t.lineOffsets = append(t.lineOffsets, t.lastOffset+i)
				t.updateTime = time.Now()
			}
		}
		t.rwMutex.Unlock()
		t.lastOffset = int(size)
		if size >= t.totalLen {
			atomic.StoreInt64(&t.fileSize, t.totalLen)
			continue
		}
		atomic.StoreInt64(&t.fileSize, size)
		time.Sleep(time.Second * 10)
	}
}
func reverseArray(arr []string) {
	for i, j := 0, len(arr)-1; i < j; i, j = i+1, j-1 {
		arr[i], arr[j] = arr[j], arr[i]
	}
}

package fsearch

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type DirSearch struct {
	rwMutex   sync.RWMutex
	fileMap   map[string]*FileSearch
	dir       string
	closeChan chan struct{}
	closeOnce sync.Once // protect closeChan
}

func NewDirSearch(dir string) (*DirSearch, error) {
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return nil, err
	}
	f := &DirSearch{
		dir:     dir,
		fileMap: make(map[string]*FileSearch, 64),
	}
	go f.detectDir()
	return f, nil
}
func (f *DirSearch) Close() {
	f.closeOnce.Do(func() {
		for _, search := range f.fileMap {
			search.Close()
		}
		close(f.closeChan)
	})
}

func (f *DirSearch) SearchFromLastFile(maxLines int, fromEnd bool, kws ...string) []string {
	var lastFileSearch *FileSearch
	var lastTime time.Time
	f.rwMutex.RLock()
	for _, s := range f.fileMap {
		if s.updateTime.After(lastTime) {
			lastTime = s.updateTime
			lastFileSearch = s
		}
	}
	f.rwMutex.RUnlock()
	if lastFileSearch == nil {
		return nil
	}
	if fromEnd {
		return lastFileSearch.SearchFromEnd(maxLines, kws...)
	}
	return lastFileSearch.SearchFromStart(maxLines, kws...)
}
func (f *DirSearch) SearchWithFileName(maxLines int, fileName string, kws ...string) []string {
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	search, ok := f.fileMap[fileName]
	if !ok {
		return nil
	}
	lines := search.SearchFromEnd(maxLines, kws...)
	return lines
}
func (f *DirSearch) SearchFromStart(maxLines int, kws ...string) []string {
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	var result []string
	var searches []*FileSearch
	for _, search := range f.fileMap {
		searches = append(searches, search)
	}
	sort.Slice(searches, func(i, j int) bool {
		return searches[i].updateTime.After(searches[j].updateTime)
	})
	for _, search := range searches {
		lines := search.SearchFromStart(maxLines, kws...)
		result = append(result, lines...)
	}
	return result
}
func (f *DirSearch) SearchFromEnd(maxLines int, kws ...string) []string {
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	var result []string
	var searches []*FileSearch
	for _, search := range f.fileMap {
		searches = append(searches, search)
	}
	sort.Slice(searches, func(i, j int) bool {
		return searches[i].updateTime.After(searches[j].updateTime)
	})
	for _, search := range searches {
		lines := search.SearchFromEnd(maxLines, kws...)
		result = append(result, lines...)
	}
	return result
}
func (f *DirSearch) SearchBy(maxLines int, end bool, fileNames []string, kws ...string) []string {
	if len(fileNames) == 0 || len(kws) == 0 {
		return nil
	}
	f.rwMutex.RLock()
	defer f.rwMutex.RUnlock()
	var result []string
	var searches []*FileSearch
	for _, search := range f.fileMap {
		searches = append(searches, search)
	}
	sort.Slice(searches, func(i, j int) bool {
		return searches[i].updateTime.After(searches[j].updateTime)
	})
	for _, search := range searches {
		var lines []string
		if end {
			lines = search.SearchFromEnd(maxLines, kws...)
		} else {
			lines = search.SearchFromStart(maxLines, kws...)
		}
		result = append(result, lines...)
	}
	return result
}

func (f *DirSearch) detectDir() {
	for {
		select {
		case <-f.closeChan:
			return
		default:
		}
		entries, err := os.ReadDir(f.dir)
		if err != nil {
			log.Println(err)
			return
		}
		entryNameMap := make(map[string]bool, len(entries))
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			entryNameMap[entry.Name()] = true
		}
		// 先剔除不存在的文件
		f.rwMutex.Lock()
		for s, search := range f.fileMap {
			if _, ok := entryNameMap[s]; !ok {
				search.Close()
				delete(f.fileMap, s)
				log.Println("fsearch: remove file", s)
			}
		}
		// 再添加新的文件
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fileName := entry.Name()
			if _, ok := f.fileMap[fileName]; ok {
				continue
			}
			filePath := filepath.Join(f.dir, fileName)
			fileSearch, err := NewFileSearch(filePath)
			if err != nil {
				log.Println(err)
				continue
			}
			f.fileMap[fileName] = fileSearch
			log.Println("fsearch: add file", fileName)
		}
		f.rwMutex.Unlock()
		time.Sleep(time.Second * 30)
	}
}

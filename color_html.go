package fsearch

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"
)

// We use upper case to make the variable exported,
// so that it can be used in other packages as well or can be modified, but not recommended.
var (
	// AllLevelMap store the color of different log level.
	AllLevelMap = map[string]string{
		"INFO":  "green",
		"ERROR": "red",
		"WARN":  "orange",
	}
	TimeColor    = "#ff00ff"   //purple
	GoFileColor  = "#00bcd4"   //cyan
	TraceIdColor = "#00a5ff"   //blue
	TracedIdDeco = "underline" //underline,
	TimeFormat   = "2006-01-02T15:04:05.000"
)

func getLevelColor(level string) (string, bool) {
	for l, color := range AllLevelMap {
		if strings.Contains(level, l) {
			return color, true
		}
	}
	return "", false
}

const replaceTraceId = "TRACE_ID"

func WriteColorHTML(isHref bool, normalColor string, fontSize int64, traceIdHref string, src io.Reader, dst io.Writer) error {
	// 读取文件内容
	scanner := bufio.NewScanner(src)
	var buf bytes.Buffer
	if isHref {
		//word-wrap:break-word;
		buf.Write([]byte("word-wrap:break-word;"))
		if fontSize > 0 {
			buf.Write([]byte(fmt.Sprintf("font-size: %dpx;", fontSize)))
		}
		if normalColor != "" {
			buf.Write([]byte(fmt.Sprintf("color: %s;", normalColor)))
		}
	}
	_, err := dst.Write([]byte(fmt.Sprintf(`<div id="fsearch" style="%s ">`, buf.String())))
	if err != nil {
		return err
	}
	for scanner.Scan() {
		buf.Reset()
		line := scanner.Text()
		ss := strings.SplitN(line, " ", 7)
		buf.WriteString("<p>")
		for idx, s := range ss {
			if color, ok := getLevelColor(s); ok {
				//buf.WriteString(fmt.Sprintf(`<font color="%s">%s </font>`, color, s))
				buf.WriteString(fmt.Sprintf(`<span style="color:%s">%s </span>`, color, s))
				continue
			}
			// parse time
			_, err = time.Parse(TimeFormat, s)
			if err == nil {
				buf.WriteString(fmt.Sprintf(`<span style="color:%s">%s </span>`, TimeColor, s))
				continue
			}
			// xxx.go:lineNo
			if strings.Contains(s, ".go:") {
				buf.WriteString(fmt.Sprintf(`<span style="color:%s">%s </span>`, GoFileColor, s))
				continue
			}
			// traceId:xxxx
			if strings.HasPrefix(s, "traceId:") {
				if idx != len(ss)-1 {
					traceId := strings.Split(s, ":")[1]
					if traceId != "" {
						href := strings.ReplaceAll(traceIdHref, replaceTraceId, traceId)
						// 防止将后面的内容也加上下划线
						buf.WriteString(fmt.Sprintf(`<a href="%s" style="color:%s ;text-decoration:%s">%s </a>`, href, TraceIdColor, TracedIdDeco, s))
						continue
					}
				}
				buf.WriteString(s)
				continue
			}
			buf.WriteString(s)
			buf.WriteString(" ")
		}
		buf.WriteString("</p>\n")
		dst.Write(buf.Bytes())
	}
	dst.Write([]byte("</div>"))
	if err = scanner.Err(); err != nil {
		return err
	}
	return nil
}

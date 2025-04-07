package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"csz.net/tgstate/assets"
	"csz.net/tgstate/conf"
	"csz.net/tgstate/utils"
)

// UploadImageAPI 上传图片api
func UploadImageAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodPost {
		// 获取上传的文件
		file, header, err := r.FormFile("image")
		if err != nil {
			errJsonMsg("Unable to get file", w)
			// http.Error(w, "Unable to get file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		if conf.Mode != "p" && r.ContentLength > 20*1024*1024 {
			// 检查文件大小
			errJsonMsg("File size exceeds 20MB limit", w)
			return
		}
		// 检查文件类型
		allowedExts := []string{".jpg", ".jpeg", ".png"}
		ext := filepath.Ext(header.Filename)
		valid := false
		for _, allowedExt := range allowedExts {
			if ext == allowedExt {
				valid = true
				break
			}
		}
		if conf.Mode != "p" && !valid {
			errJsonMsg("Invalid file type. Only .jpg, .jpeg, and .png are allowed.", w)
			// http.Error(w, "Invalid file type. Only .jpg, .jpeg, and .png are allowed.", http.StatusBadRequest)
			return
		}
		res := conf.UploadResponse{
			Code:    0,
			Message: "error",
		}
		img := conf.FileRoute + utils.UpDocument(utils.TgFileData(header.Filename, file))
		if img != conf.FileRoute {
			res = conf.UploadResponse{
				Code:    1,
				Message: img,
				ImgUrl:  strings.TrimSuffix(conf.BaseUrl, "/") + img,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(res)
		return
	}

	// 如果不是POST请求，返回错误响应
	http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
}
func errJsonMsg(msg string, w http.ResponseWriter) {
	// 这里示例直接返回JSON响应
	response := conf.UploadResponse{
		Code:    0,
		Message: msg,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
func D(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	id := strings.TrimPrefix(path, conf.FileRoute)
	if id == "" {
		// 设置响应的状态码为 404
		w.WriteHeader(http.StatusNotFound)
		// 写入响应内容
		w.Write([]byte("404 Not Found"))
		return
	}

	// 发起HTTP GET请求来获取Telegram图片
	fileUrl, _ := utils.GetDownloadUrl(id)
	resp, err := http.Get(fileUrl)
	if err != nil {
		http.Error(w, "Failed to fetch content", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	
	// 检查Content-Type是否为图片类型
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/octet-stream") {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("404 Not Found"))
		return
	}
	
	contentLength, err := strconv.Atoi(resp.Header.Get("Content-Length"))
	if err != nil {
		log.Println("获取Content-Length出错:", err)
		return
	}
	
	// 读取内容到缓冲区
	buffer := make([]byte, contentLength)
	n, err := resp.Body.Read(buffer)
	if err != nil && err != io.ErrUnexpectedEOF {
		log.Println("读取响应主体数据时发生错误:", err)
		return
	}
	
	// 处理分块文件
	if string(buffer[:12]) == "tgstate-blob" {
		content := string(buffer)
		lines := strings.Split(content, "\n")
		log.Println("分块文件:" + lines[1])
		var fileSize string
		var startLine = 2
		if strings.HasPrefix(lines[2], "size") {
			fileSize = lines[2][len("size"):]
			startLine = startLine + 1
		}
		
		// 设置响应头
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+lines[1]+"\"")
		w.Header().Set("Content-Length", fileSize)
		w.Header().Set("Accept-Ranges", "bytes")
		
		// 处理分块文件的下载
		handleBlobDownload(w, r, lines, startLine, fileSize)
	} else {
		// 处理单个文件
		contentType := http.DetectContentType(buffer)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(n))
		w.Header().Set("Accept-Ranges", "bytes")
		
		// 处理视频和音频文件的Range请求
		if strings.HasPrefix(contentType, "video/") || strings.HasPrefix(contentType, "audio/") {
			handleRangeRequest(w, r, buffer[:n])
		} else {
			// 对于其他类型的文件，直接发送
			w.Write(buffer[:n])
		}
	}
}

// 处理Range请求
func handleRangeRequest(w http.ResponseWriter, r *http.Request, data []byte) {
	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		// 如果没有Range头，发送整个文件
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}
	
	// 解析Range头
	ranges, err := parseRange(rangeHeader, int64(len(data)))
	if err != nil || len(ranges) != 1 {
		// 如果Range头无效，发送整个文件
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}
	
	// 获取请求的范围
	ra := ranges[0]
	
	// 设置部分内容响应
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", ra.start, ra.end, len(data)))
	w.Header().Set("Content-Length", strconv.FormatInt(ra.length, 10))
	w.WriteHeader(http.StatusPartialContent)
	
	// 发送请求的部分内容
	w.Write(data[ra.start:ra.end+1])
}

// 处理分块文件的下载
func handleBlobDownload(w http.ResponseWriter, r *http.Request, lines []string, startLine int, fileSize string) {
	// 目前不支持分块文件的Range请求，直接发送所有块
	for i := startLine; i < len(lines); i++ {
		fileStatus := false
		var fileUrl string
		var reTry = 0
		for !fileStatus {
			if reTry > 0 {
				time.Sleep(5 * time.Second)
			}
			reTry = reTry + 1
			fileUrl, fileStatus = utils.GetDownloadUrl(strings.ReplaceAll(lines[i], " ", ""))
		}
		blobResp, err := http.Get(fileUrl)
		if err != nil {
			http.Error(w, "Failed to fetch content", http.StatusInternalServerError)
			return
		}
		_, err = io.Copy(w, blobResp.Body)
		blobResp.Body.Close()
		if err != nil {
			log.Println("写入响应主体数据时发生错误:", err)
			return
		}
	}
}

// 解析Range头
type httpRange struct {
	start, end, length int64
}

func parseRange(s string, size int64) ([]httpRange, error) {
	if s == "" {
		return nil, nil // 没有Range头
	}
	
	const b = "bytes="
	if !strings.HasPrefix(s, b) {
		return nil, errors.New("invalid range")
	}
	
	var ranges []httpRange
	noOverlap := false
	for _, ra := range strings.Split(s[len(b):], ",") {
		ra = strings.TrimSpace(ra)
		if ra == "" {
			continue
		}
		
		i := strings.Index(ra, "-")
		if i < 0 {
			return nil, errors.New("invalid range")
		}
		
		start, end := strings.TrimSpace(ra[:i]), strings.TrimSpace(ra[i+1:])
		var r httpRange
		
		if start == "" {
			// 如果没有开始位置，例如 -100，表示最后100个字节
			i, err := strconv.ParseInt(end, 10, 64)
			if err != nil {
				return nil, errors.New("invalid range")
			}
			if i > size {
				i = size
			}
			r.start = size - i
			r.end = size - 1
		} else {
			i, err := strconv.ParseInt(start, 10, 64)
			if err != nil || i >= size || i < 0 {
				return nil, errors.New("invalid range")
			}
			r.start = i
			
			if end == "" {
				// 如果没有结束位置，例如 100-，表示从100到结束
				r.end = size - 1
			} else {
				i, err := strconv.ParseInt(end, 10, 64)
				if err != nil || i >= size || i < 0 {
					return nil, errors.New("invalid range")
				}
				if i < r.start {
					return nil, errors.New("invalid range")
				}
				r.end = i
			}
		}
		
		r.length = r.end - r.start + 1
		ranges = append(ranges, r)
		
		if r.start == 0 && r.end == size-1 {
			noOverlap = true
			break
		}
	}
	
	if noOverlap && len(ranges) > 1 {
		// 如果有一个范围覆盖了整个文件，忽略其他范围
		return ranges[:1], nil
	}
	
	return ranges, nil
}

// Index 首页
func Index(w http.ResponseWriter, r *http.Request) {
	htmlPath := "templates/images.tmpl"
	if conf.Mode == "p" {
		htmlPath = "templates/files.tmpl"
	}
	file, err := assets.Templates.ReadFile(htmlPath)
	if err != nil {
		http.Error(w, "HTML file not found", http.StatusNotFound)
		return
	}
	// 读取头部模板
	headerFile, err := assets.Templates.ReadFile("templates/header.tmpl")
	if err != nil {
		http.Error(w, "Header template not found", http.StatusNotFound)
		return
	}

	// 读取页脚模板
	footerFile, err := assets.Templates.ReadFile("templates/footer.tmpl")
	if err != nil {
		http.Error(w, "Footer template not found", http.StatusNotFound)
		return
	}

	// 创建HTML模板并包括头部
	tmpl := template.New("html")
	tmpl, err = tmpl.Parse(string(headerFile))
	if err != nil {
		http.Error(w, "Error parsing header template", http.StatusInternalServerError)
		return
	}

	// 包括主HTML内容
	tmpl, err = tmpl.Parse(string(file))
	if err != nil {
		http.Error(w, "Error parsing HTML template", http.StatusInternalServerError)
		return
	}

	// 包括页脚
	tmpl, err = tmpl.Parse(string(footerFile))
	if err != nil {
		http.Error(w, "Error parsing footer template", http.StatusInternalServerError)
		return
	}

	// 直接将HTML内容发送给客户端
	w.Header().Set("Content-Type", "text/html")
	err = tmpl.Execute(w, nil)
	if err != nil {
		http.Error(w, "Error rendering HTML template", http.StatusInternalServerError)
	}
}

func Pwd(w http.ResponseWriter, r *http.Request) {
	// 输出 HTML 表单
	if r.Method != http.MethodPost {
		file, err := assets.Templates.ReadFile("templates/pwd.tmpl")
		if err != nil {
			http.Error(w, "HTML file not found", http.StatusNotFound)
			return
		}
		// 读取头部模板
		headerFile, err := assets.Templates.ReadFile("templates/header.tmpl")
		if err != nil {
			http.Error(w, "Header template not found", http.StatusNotFound)
			return
		}

		// 创建HTML模板并包括头部
		tmpl := template.New("html")
		if tmpl, err = tmpl.Parse(string(headerFile)); err != nil {
			http.Error(w, "Error parsing Header template", http.StatusInternalServerError)
			return
		}

		// 包括主HTML内容
		if tmpl, err = tmpl.Parse(string(file)); err != nil {
			http.Error(w, "Error parsing File template", http.StatusInternalServerError)
			return
		}

		// 直接将HTML内容发送给客户端
		w.Header().Set("Content-Type", "text/html")
		if err := tmpl.Execute(w, nil); err != nil {
			http.Error(w, "Error rendering HTML template", http.StatusInternalServerError)
		}
		return
	}
	// 设置cookie
	cookie := http.Cookie{
		Name:  "p",
		Value: r.FormValue("p"),
	}
	http.SetCookie(w, &cookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 只有当密码设置并且不为"none"时，才进行检查
		if conf.Pass != "" && conf.Pass != "none" {
			if strings.HasPrefix(r.URL.Path, "/api") && r.URL.Query().Get("pass") == conf.Pass {
				return
			}
			if cookie, err := r.Cookie("p"); err != nil || cookie.Value != conf.Pass {
				http.Redirect(w, r, "/pwd", http.StatusSeeOther)
				return
			}
		}
		next(w, r)
	}
}

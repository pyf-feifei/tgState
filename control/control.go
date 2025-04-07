package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"csz.net/tgstate/assets"
	"csz.net/tgstate/conf"
	"csz.net/tgstate/utils"
)

// 文件缓存结构
type FileCache struct {
	sync.RWMutex
	files      map[string]string // fileID -> 本地文件路径
	lastAccess map[string]int64  // fileID -> 最后访问时间
	cacheDir   string            // 缓存目录
}

var (
	fileCache *FileCache
	once      sync.Once
)

// 获取文件缓存单例
func getFileCache() *FileCache {
	once.Do(func() {
		cacheDir := filepath.Join(".", "file_cache")
		if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
			os.MkdirAll(cacheDir, 0755)
		}
		fileCache = &FileCache{
			files:      make(map[string]string),
			lastAccess: make(map[string]int64),
			cacheDir:   cacheDir,
		}
		// 启动定期清理协程
		go fileCache.periodicCleanup()
	})
	return fileCache
}

// 获取缓存文件，如果不存在则下载
func (fc *FileCache) getCachedFile(fileID string) (string, error) {
	// 检查缓存
	fc.RLock()
	filePath, exists := fc.files[fileID]
	fc.RUnlock()

	if exists {
		// 检查文件是否存在
		if _, err := os.Stat(filePath); err == nil {
			// 更新最后访问时间
			fc.Lock()
			fc.lastAccess[fileID] = time.Now().Unix()
			fc.Unlock()
			return filePath, nil
		}
	}

	// 缓存不存在或文件已删除，下载文件
	fileURL, ok := utils.GetDownloadUrl(fileID)
	if !ok {
		return "", fmt.Errorf("获取文件下载链接失败")
	}

	// 创建缓存文件
	filePath = filepath.Join(fc.cacheDir, fileID)
	out, err := os.Create(filePath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	// 下载文件
	resp, err := http.Get(fileURL)
	if err != nil {
		os.Remove(filePath)
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		os.Remove(filePath)
		return "", fmt.Errorf("下载文件失败，状态码: %d", resp.StatusCode)
	}

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		os.Remove(filePath)
		return "", err
	}

	// 更新缓存
	fc.Lock()
	fc.files[fileID] = filePath
	fc.lastAccess[fileID] = time.Now().Unix()
	fc.Unlock()

	return filePath, nil
}

// 清理指定文件
func (fc *FileCache) cleanupFile(fileID string) {
	fc.Lock()
	filePath, exists := fc.files[fileID]
	if exists {
		delete(fc.files, fileID)
		delete(fc.lastAccess, fileID)
	}
	fc.Unlock()
	
	if exists && filePath != "" {
		os.Remove(filePath)
		log.Printf("已清理缓存文件: %s", fileID)
	}
}

// 定期清理过期缓存
func (fc *FileCache) periodicCleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		now := time.Now().Unix()
		expireTime := now - 3600 // 1小时未访问的文件将被清理
		
		var filesToDelete []string
		var idsToDelete []string
		
		fc.RLock()
		for fileID, lastAccess := range fc.lastAccess {
			if lastAccess < expireTime {
				if filePath, ok := fc.files[fileID]; ok {
					filesToDelete = append(filesToDelete, filePath)
					idsToDelete = append(idsToDelete, fileID)
				}
			}
		}
		fc.RUnlock()
		
		// 删除文件
		for _, filePath := range filesToDelete {
			os.Remove(filePath)
		}
		
		// 更新缓存映射
		fc.Lock()
		for _, fileID := range idsToDelete {
			delete(fc.files, fileID)
			delete(fc.lastAccess, fileID)
		}
		fc.Unlock()
		
		if len(idsToDelete) > 0 {
			log.Printf("已清理 %d 个过期缓存文件", len(idsToDelete))
		}
	}
}

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

	// 获取文件缓存
	cache := getFileCache()
	
	// 检查是否为分块文件
	if strings.HasPrefix(id, "blob-") {
		// 处理分块文件
		handleBlobFile(w, r, id)
		return
	}
	
	// 从缓存获取文件
	filePath, err := cache.getCachedFile(id)
	if err != nil {
		log.Printf("获取文件失败: %v", err)
		http.Error(w, "Failed to fetch content", http.StatusInternalServerError)
		return
	}
	
	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("打开文件失败: %v", err)
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	
	// 获取文件信息
	fileInfo, err := file.Stat()
	if err != nil {
		log.Printf("获取文件信息失败: %v", err)
		http.Error(w, "Failed to get file info", http.StatusInternalServerError)
		return
	}
	fileSize := fileInfo.Size()
	
	// 读取文件头部以检测内容类型
	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil && err != io.EOF {
		log.Printf("读取文件头部失败: %v", err)
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}
	// 重置文件指针
	file.Seek(0, io.SeekStart)
	
	// 检测内容类型
	contentType := http.DetectContentType(buffer)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))
	
	// 判断是否为视频文件
	isVideo := strings.HasPrefix(contentType, "video/")
	
	// 只有视频文件才处理Range请求
	if isVideo {
		w.Header().Set("Accept-Ranges", "bytes")
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			// 解析Range头
			ranges, err := parseRange(rangeHeader, fileSize)
			if err != nil || len(ranges) != 1 {
				// 如果Range头无效，发送整个文件
				io.Copy(w, file)
				return
			}
			
			// 获取请求的范围
			ra := ranges[0]
			
			// 设置文件指针到请求的起始位置
			file.Seek(ra.start, io.SeekStart)
			
			// 设置部分内容响应
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", ra.start, ra.end, fileSize))
			w.Header().Set("Content-Length", strconv.FormatInt(ra.length, 10))
			w.WriteHeader(http.StatusPartialContent)
			
			// 发送请求的部分内容
			io.CopyN(w, file, ra.length)
			
			// 检查是否是最后一个Range请求（通常是视频播放结束）
			if ra.end >= fileSize-1 || ra.end >= fileSize-1024*1024 { // 文件结尾或接近结尾
				// 延迟清理文件，给予一些缓冲时间
				go func() {
					time.Sleep(10 * time.Second) // 等待10秒，确保没有新请求
					cache.cleanupFile(id)
				}()
			}
			
			return
		}
	} else {
		// 非视频文件不支持Range请求
		w.Header().Set("Accept-Ranges", "none")
	}
	
	// 非Range请求或非视频文件，发送整个文件
	io.Copy(w, file)
	
	// 对于非视频文件，请求完成后标记为可清理
	if !isVideo {
		go func() {
			time.Sleep(5 * time.Second) // 等待5秒，确保浏览器已完成处理
			cache.cleanupFile(id)
		}()
	}
}

// 处理分块文件
func handleBlobFile(w http.ResponseWriter, r *http.Request, blobID string) {
	// 获取分块文件信息
	// 这里需要根据您的实际情况实现
	// ...
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

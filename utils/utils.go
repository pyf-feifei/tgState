package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"csz.net/tgstate/conf"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// FileCache 文件缓存结构
type FileCache struct {
	sync.RWMutex
	files       map[string]string // fileID -> 本地文件路径
	lastAccess  map[string]int64  // fileID -> 最后访问时间
	cacheDir    string            // 缓存目录
	cleanupLock sync.Mutex        // 清理锁
}

var (
	fileCache *FileCache
	once      sync.Once
)

// GetFileCache 获取文件缓存单例
func GetFileCache() *FileCache {
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

// GetCachedFile 获取缓存文件，如果不存在则下载
func (fc *FileCache) GetCachedFile(fileID string) (string, error) {
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
	fileURL, ok := GetDownloadUrl(fileID)
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

// MarkFileForCleanup 标记文件可以清理
func (fc *FileCache) MarkFileForCleanup(fileID string) {
	fc.Lock()
	defer fc.Unlock()
	
	// 设置最后访问时间为过去时间，使其在下次清理时被删除
	fc.lastAccess[fileID] = 1
}

// CleanupFile 立即清理指定文件
func (fc *FileCache) CleanupFile(fileID string) {
	fc.Lock()
	filePath, exists := fc.files[fileID]
	if exists {
		delete(fc.files, fileID)
		delete(fc.lastAccess, fileID)
	}
	fc.Unlock()
	
	if exists && filePath != "" {
		os.Remove(filePath)
	}
}

// periodicCleanup 定期清理过期缓存
func (fc *FileCache) periodicCleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		fc.cleanupExpiredFiles()
	}
}

// cleanupExpiredFiles 清理过期文件
func (fc *FileCache) cleanupExpiredFiles() {
	fc.cleanupLock.Lock()
	defer fc.cleanupLock.Unlock()
	
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

// 以下是原有函数

func TgFileData(fileName string, fileData io.Reader) tgbotapi.FileReader {
	return tgbotapi.FileReader{
		Name:   fileName,
		Reader: fileData,
	}
}

func UpDocument(fileData tgbotapi.FileReader) string {
	bot, err := tgbotapi.NewBotAPI(conf.BotToken)
	if err != nil {
		log.Println(err)
		return ""
	}
	// Upload the file to Telegram
	params := tgbotapi.Params{
		"chat_id": conf.ChannelName, // Replace with the chat ID where you want to send the file
	}
	files := []tgbotapi.RequestFile{
		{
			Name: "document",
			Data: fileData,
		},
	}
	response, err := bot.UploadFiles("sendDocument", params, files)
	if err != nil {
		log.Panic(err)
	}
	var msg tgbotapi.Message
	json.Unmarshal([]byte(response.Result), &msg)
	var resp string
	switch {
	case msg.Document != nil:
		resp = msg.Document.FileID
	case msg.Audio != nil:
		resp = msg.Audio.FileID
	case msg.Video != nil:
		resp = msg.Video.FileID
	case msg.Sticker != nil:
		resp = msg.Sticker.FileID
	}
	return resp
}

func GetDownloadUrl(fileID string) (string, bool) {
	bot, err := tgbotapi.NewBotAPI(conf.BotToken)
	if err != nil {
		log.Panic(err)
	}
	// 使用 getFile 方法获取文件信息
	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		log.Println("获取文件失败【" + fileID + "】")
		log.Println(err)
		return "", false
	}
	log.Println("获取文件成功【" + fileID + "】")
	// 获取文件下载链接
	fileURL := file.Link(conf.BotToken)
	return fileURL, true
}

func BotDo() {
	bot, err := tgbotapi.NewBotAPI(conf.BotToken)
	if err != nil {
		log.Println(err)
		return
	}
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updatesChan := bot.GetUpdatesChan(u)
	for update := range updatesChan {
		var msg *tgbotapi.Message
		if update.Message != nil {
			msg = update.Message
		}
		if update.ChannelPost != nil {
			msg = update.ChannelPost
		}
		if msg != nil && msg.Text == "get" && msg.ReplyToMessage != nil {
			var fileID string
			switch {
			case msg.ReplyToMessage.Document != nil && msg.ReplyToMessage.Document.FileID != "":
				fileID = msg.ReplyToMessage.Document.FileID
			case msg.ReplyToMessage.Video != nil && msg.ReplyToMessage.Video.FileID != "":
				fileID = msg.ReplyToMessage.Video.FileID
			case msg.ReplyToMessage.Sticker != nil && msg.ReplyToMessage.Sticker.FileID != "":
				fileID = msg.ReplyToMessage.Sticker.FileID
			}
			if fileID != "" {
				newMsg := tgbotapi.NewMessage(msg.Chat.ID, strings.TrimSuffix(conf.BaseUrl, "/")+"/d/"+fileID)
				newMsg.ReplyToMessageID = msg.MessageID
				if !strings.HasPrefix(conf.ChannelName, "@") {
					if man, err := strconv.Atoi(conf.ChannelName); err == nil && int(msg.Chat.ID) == man {
						bot.Send(newMsg)
					}
				} else {
					bot.Send(newMsg)
				}
			}
		}
	}
}

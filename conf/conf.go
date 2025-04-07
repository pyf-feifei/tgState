package conf

var BotToken string
var ChannelName string
var Pass string
var Mode string
var BaseUrl string
var TgBotApiProxy string  // 新增变量，用于存储 Telegram Bot API 代理地址

type UploadResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	ImgUrl  string `json:"url"`
}

const FileRoute = "/d/"

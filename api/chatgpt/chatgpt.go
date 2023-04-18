package chatgpt

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/linweiyuan/go-chatgpt-api/api"
	"github.com/linweiyuan/go-chatgpt-api/util/logger"
	"github.com/linweiyuan/go-chatgpt-api/webdriver"
	"github.com/tebeka/selenium"
)

const (
	apiPrefix                      = "https://chat.openai.com/backend-api"
	defaultRole                    = "user"
	getConversationsErrorMessage   = "Failed to get conversations."
	generateTitleErrorMessage      = "Failed to generate title."
	getContentErrorMessage         = "Failed to get content."
	updateConversationErrorMessage = "Failed to update conversation."
	clearConversationsErrorMessage = "Failed to clear conversations."
	feedbackMessageErrorMessage    = "Failed to add feedback."
	getModelsErrorMessage          = "Failed to get models."
	doneFlag                       = "[DONE]"
)

var mutex sync.Mutex

//goland:noinspection GoUnhandledErrorResult
func init() {
	go func() {
		ticker := time.NewTicker(api.RefreshEveryMinutes * time.Minute)

		for {
			select {
			case <-ticker.C:
				tryToRefreshPage()
			}
		}
	}()
}

func tryToRefreshPage() {
	defer func() {
		if err := recover(); err != nil {
			logger.Error("Failed to refresh page")
			mutex.Unlock()
		}
	}()

	if mutex.TryLock() {
		webdriver.Refresh()
		mutex.Unlock()
	}
}

//goland:noinspection GoUnhandledErrorResult
func GetConversations(c *gin.Context) {
	offset, ok := c.GetQuery("offset")
	if !ok {
		offset = "0"
	}
	limit, ok := c.GetQuery("limit")
	if !ok {
		limit = "20"
	}
	url := apiPrefix + "/conversations?offset=" + offset + "&limit=" + limit
	accessToken := api.GetAccessToken(c.GetHeader(api.AuthorizationHeader))
	script := getGetScript(url, accessToken, getConversationsErrorMessage)
	responseText, err := webdriver.WebDriver.ExecuteScriptAsync(script, nil)
	if handleSeleniumError(err, script, c) {
		return
	}

	if responseText == getConversationsErrorMessage {
		tryToRefreshPage()
		c.JSON(http.StatusInternalServerError, api.ReturnMessage(getConversationsErrorMessage))
		return
	}

	c.Writer.Write([]byte(responseText.(string)))
}

type StartConversationRequest struct {
	Action          string    `json:"action"`
	Messages        []Message `json:"messages"`
	Model           string    `json:"model"`
	ParentMessageID string    `json:"parent_message_id"`
	ConversationID  *string   `json:"conversation_id"`
	ContinueText    string    `json:"continue_text"`
}

type Message struct {
	Author  Author  `json:"author"`
	Content Content `json:"content"`
	ID      string  `json:"id"`
}

type Author struct {
	Role string `json:"role"`
}

type Content struct {
	ContentType string   `json:"content_type"`
	Parts       []string `json:"parts"`
}

type ConversationResponse struct {
	Message struct {
		ID      string `json:"id"`
		Content struct {
			Parts []string `json:"parts"`
		} `json:"content"`
		EndTurn  bool `json:"end_turn"`
		Metadata struct {
			FinishDetails struct {
				Type string `json:"type"`
			} `json:"finish_details"`
		} `json:"metadata"`
	} `json:"message"`
	ConversationID string `json:"conversation_id"`
}

//goland:noinspection GoUnhandledErrorResult
func StartConversation(c *gin.Context) {
	mutex.Lock()
	defer mutex.Unlock()

	var callbackChannel = make(chan string)

	var request StartConversationRequest
	c.BindJSON(&request)
	if request.ConversationID == nil || *request.ConversationID == "" {
		request.ConversationID = nil
	}
	if request.Messages[0].Author.Role == "" {
		request.Messages[0].Author.Role = defaultRole
	}

	oldContentToResponse := ""
	if !sendConversationRequest(c, callbackChannel, request, oldContentToResponse) {
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	for eventDataString := range callbackChannel {
		c.Writer.Write([]byte("data: " + eventDataString + "\n\n"))
		c.Writer.Flush()
	}
}

//goland:noinspection GoUnhandledErrorResult
func sendConversationRequest(c *gin.Context, callbackChannel chan string, request StartConversationRequest, oldContent string) bool {
	jsonBytes, _ := json.Marshal(request)
	url := apiPrefix + "/conversation"
	accessToken := api.GetAccessToken(c.GetHeader(api.AuthorizationHeader))
	script := getPostScriptForStartConversation(url, accessToken, string(jsonBytes))
	_, err := webdriver.WebDriver.ExecuteScript(script, nil)
	if handleSeleniumError(err, script, c) {
		return false
	}

	go func() {
		webdriver.WebDriver.ExecuteScript("delete window.conversationResponseData;", nil)
		temp := ""
		var conversationResponse ConversationResponse
		maxTokens := false
		for {
			conversationResponseData, _ := webdriver.WebDriver.ExecuteScript("return window.conversationResponseData;", nil)
			if conversationResponseData == nil || conversationResponseData == "" {
				continue
			}

			conversationResponseDataString := conversationResponseData.(string)
			if conversationResponseDataString[0:1] == strconv.Itoa(4) || conversationResponseDataString[0:1] == strconv.Itoa(5) {
				statusCode, _ := strconv.Atoi(conversationResponseDataString[0:3])
				if statusCode == http.StatusForbidden {
					webdriver.Refresh()
				}
				c.AbortWithStatusJSON(statusCode, api.ReturnMessage(conversationResponseDataString[3:]))
				close(callbackChannel)
				break
			}

			if conversationResponseDataString[0:1] == "!" {
				callbackChannel <- conversationResponseDataString[1:]
				callbackChannel <- doneFlag
				close(callbackChannel)
				break
			}

			if temp != "" {
				if temp == conversationResponseDataString {
					continue
				}
			}
			temp = conversationResponseDataString

			err := json.Unmarshal([]byte(conversationResponseDataString), &conversationResponse)
			if err != nil {
				logger.Info(conversationResponseDataString)
				logger.Error(err.Error())
				continue
			}

			message := conversationResponse.Message
			if oldContent == "" {
				callbackChannel <- conversationResponseDataString
			} else {
				message.Content.Parts[0] = oldContent + (message.Content.Parts[0])
				withOldContentJsonString, _ := json.Marshal(conversationResponse)
				callbackChannel <- string(withOldContentJsonString)
			}

			maxTokens = message.Metadata.FinishDetails.Type == "max_tokens"
			if maxTokens {
				if request.ContinueText == "" {
					callbackChannel <- doneFlag
					close(callbackChannel)
				} else {
					oldContent = message.Content.Parts[0]
				}
				break
			}

			endTurn := message.EndTurn
			if endTurn {
				callbackChannel <- doneFlag
				close(callbackChannel)
				break
			}
		}
		if maxTokens && request.ContinueText != "" {
			time.Sleep(time.Second)

			parentMessageID := conversationResponse.Message.ID
			conversationID := conversationResponse.ConversationID
			requestBodyJson := fmt.Sprintf(`
			{
				"action": "next",
				"messages": [{
					"id": "%s",
					"author": {
						"role": "%s"
					},
					"role": "%s",
					"content": {
						"content_type": "text",
						"parts": ["%s"]
					}
				}],
				"parent_message_id": "%s",
				"model": "%s",
				"conversation_id": "%s",
				"continue_text": "%s"
			}`, uuid.NewString(), defaultRole, defaultRole, request.ContinueText, parentMessageID, request.Model, conversationID, request.ContinueText)
			var request StartConversationRequest
			json.Unmarshal([]byte(requestBodyJson), &request)
			sendConversationRequest(c, callbackChannel, request, oldContent)
		}
	}()
	return true
}

type GenerateTitleRequest struct {
	MessageID string `json:"message_id"`
	Model     string `json:"model"`
}

//goland:noinspection GoUnhandledErrorResult
func GenerateTitle(c *gin.Context) {
	var request GenerateTitleRequest
	c.BindJSON(&request)
	jsonBytes, _ := json.Marshal(request)
	url := apiPrefix + "/conversation/gen_title/" + c.Param("id")
	accessToken := api.GetAccessToken(c.GetHeader(api.AuthorizationHeader))
	script := getPostScript(url, accessToken, string(jsonBytes), generateTitleErrorMessage)
	responseText, err := webdriver.WebDriver.ExecuteScriptAsync(script, nil)
	if handleSeleniumError(err, script, c) {
		return
	}

	if responseText == generateTitleErrorMessage {
		tryToRefreshPage()
		c.JSON(http.StatusInternalServerError, api.ReturnMessage(generateTitleErrorMessage))
		return
	}

	c.Writer.Write([]byte(responseText.(string)))
}

//goland:noinspection GoUnhandledErrorResult
func GetConversation(c *gin.Context) {
	url := apiPrefix + "/conversation/" + c.Param("id")
	accessToken := api.GetAccessToken(c.GetHeader(api.AuthorizationHeader))
	script := getGetScript(url, accessToken, getContentErrorMessage)
	responseText, err := webdriver.WebDriver.ExecuteScriptAsync(script, nil)
	if handleSeleniumError(err, script, c) {
		return
	}

	if responseText == getContentErrorMessage {
		tryToRefreshPage()
		c.JSON(http.StatusInternalServerError, api.ReturnMessage(getContentErrorMessage))
		return
	}

	c.Writer.Write([]byte(responseText.(string)))
}

type PatchConversationRequest struct {
	Title     *string `json:"title"`
	IsVisible bool    `json:"is_visible"`
}

//goland:noinspection GoUnhandledErrorResult
func UpdateConversation(c *gin.Context) {
	var request PatchConversationRequest
	c.BindJSON(&request)
	// bool default to false, then will hide (delete) the conversation
	if request.Title != nil {
		request.IsVisible = true
	}
	jsonBytes, _ := json.Marshal(request)
	url := apiPrefix + "/conversation/" + c.Param("id")
	accessToken := api.GetAccessToken(c.GetHeader(api.AuthorizationHeader))
	script := getPatchScript(url, accessToken, string(jsonBytes), updateConversationErrorMessage)
	responseText, err := webdriver.WebDriver.ExecuteScriptAsync(script, nil)
	if handleSeleniumError(err, script, c) {
		return
	}

	if responseText == updateConversationErrorMessage {
		tryToRefreshPage()
		c.JSON(http.StatusInternalServerError, api.ReturnMessage(updateConversationErrorMessage))
		return
	}

	c.Writer.Write([]byte(responseText.(string)))
}

type FeedbackMessageRequest struct {
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	Rating         string `json:"rating"`
}

//goland:noinspection GoUnhandledErrorResult
func FeedbackMessage(c *gin.Context) {
	var request FeedbackMessageRequest
	c.BindJSON(&request)
	jsonBytes, _ := json.Marshal(request)
	url := apiPrefix + "/conversation/message_feedback"
	accessToken := api.GetAccessToken(c.GetHeader(api.AuthorizationHeader))
	script := getPostScript(url, accessToken, string(jsonBytes), feedbackMessageErrorMessage)
	responseText, err := webdriver.WebDriver.ExecuteScriptAsync(script, nil)
	if handleSeleniumError(err, script, c) {
		return
	}

	if responseText == feedbackMessageErrorMessage {
		tryToRefreshPage()
		c.JSON(http.StatusInternalServerError, api.ReturnMessage(feedbackMessageErrorMessage))
		return
	}

	c.Writer.Write([]byte(responseText.(string)))
}

//goland:noinspection GoUnhandledErrorResult
func ClearConversations(c *gin.Context) {
	jsonBytes, _ := json.Marshal(PatchConversationRequest{
		IsVisible: false,
	})
	url := apiPrefix + "/conversations"
	accessToken := api.GetAccessToken(c.GetHeader(api.AuthorizationHeader))
	script := getPatchScript(url, accessToken, string(jsonBytes), clearConversationsErrorMessage)
	responseText, err := webdriver.WebDriver.ExecuteScriptAsync(script, nil)
	if handleSeleniumError(err, script, c) {
		return
	}

	if responseText == clearConversationsErrorMessage {
		tryToRefreshPage()
		c.JSON(http.StatusInternalServerError, api.ReturnMessage(clearConversationsErrorMessage))
		return
	}

	c.Writer.Write([]byte(responseText.(string)))
}

func getGetScript(url string, accessToken string, errorMessage string) string {
	return fmt.Sprintf(`
		fetch('%s', {
			headers: {
				'Authorization': '%s'
			}
		})
		.then(response => {
			if (!response.ok) {
				throw new Error('%s');
			}
			return response.text();
		})
		.then(text => {
			arguments[0](text);
		})
		.catch(err => {
			arguments[0](err.message);
		});
	`, url, accessToken, errorMessage)
}

func getPostScriptForStartConversation(url string, accessToken string, jsonString string) string {
	return fmt.Sprintf(`
		// get the whole data again to make sure get the endTurn message back
		const getEndTurnMessage = (dataArray) => {
			dataArray.pop(); // empty
			dataArray.pop(); // data: [DONE]
			return '!' + dataArray.pop().substring(6); // endTurn message
		};

		let conversationResponseData;

		const xhr = new XMLHttpRequest();
		xhr.open('POST', '%s');
		xhr.setRequestHeader('Accept', 'text/event-stream');
		xhr.setRequestHeader('Authorization', '%s');
		xhr.setRequestHeader('Content-Type', 'application/json');
		xhr.onreadystatechange = function() {
			switch (xhr.readyState) {
				case xhr.LOADING: {
					switch (xhr.status) {
						case 200: {
							const dataArray = xhr.responseText.substr(xhr.seenBytes).split("\n\n");
							dataArray.pop(); // empty string
							if (dataArray.length) {
								let data = dataArray.pop(); // target data
								if (data === 'data: [DONE]') { // this DONE will break the ending handling
									data = getEndTurnMessage(xhr.responseText.split("\n\n"));
								} else if (data.startsWith('event')) {
									data = data.substring(49);
								}
								if (data) {
									if (data.startsWith('!')) {
										window.conversationResponseData = data;
									} else {
										window.conversationResponseData = data.substring(6);
									}
								}
							}
							break;
						}
						case 401: {
							window.conversationResponseData = xhr.status + 'Access token has expired.';
							break;
						}
						case 403: {
							window.conversationResponseData = xhr.status + 'Something went wrong. If this issue persists please contact us through our help center at help.openai.com.';
							break;
						}
						case 413: {
							window.conversationResponseData = xhr.status + JSON.parse(xhr.responseText).detail.message;
							break;
						}
						case 422: {
							const detail = JSON.parse(xhr.responseText).detail[0];
							window.conversationResponseData = xhr.status + detail.loc + ' -> ' + detail.msg;
							break;
						}
						case 429: {
							window.conversationResponseData = xhr.status + JSON.parse(xhr.responseText).detail;
							break;
						}
						case 500: {
							window.conversationResponseData = xhr.status + 'Unknown error.';
							break;
						}
					}
					xhr.seenBytes = xhr.responseText.length;
					break;
				}
				case xhr.DONE:
					// keep exception handling
					if (!window.conversationResponseData.startsWith('4') && !window.conversationResponseData.startsWith('5')) {
						window.conversationResponseData = getEndTurnMessage(xhr.responseText.split("\n\n"));
					}
					break;
			}
		};
		xhr.send(JSON.stringify(%s));
	`, url, accessToken, jsonString)
}

func getPostScript(url string, accessToken string, jsonString string, errorMessage string) string {
	return fmt.Sprintf(`
		fetch('%s', {
			method: 'POST',
			headers: {
				'Authorization': '%s',
				'Content-Type': 'application/json'
			},
			body: JSON.stringify(%s)
		})
		.then(response => {
			if (!response.ok) {
				throw new Error('%s');
			}
			return response.text();
		})
		.then(text => {
			arguments[0](text);
		})
		.catch(err => {
			arguments[0](err.message);
		});
	`, url, accessToken, jsonString, errorMessage)
}
func getPatchScript(url string, accessToken string, jsonString string, errorMessage string) string {
	return fmt.Sprintf(`
		fetch('%s', {
			method: 'PATCH',
			headers: {
				'Authorization': '%s',
				'Content-Type': 'application/json'
			},
			body: JSON.stringify(%s)
		})
		.then(response => {
			if (!response.ok) {
				throw new Error('%s');
			}
			return response.text();
		})
		.then(text => {
			arguments[0](text);
		})
		.catch(err => {
			arguments[0](err.message);
		});
	`, url, accessToken, jsonString, errorMessage)
}

//goland:noinspection GoUnhandledErrorResult
func handleSeleniumError(err error, script string, c *gin.Context) bool {
	if err != nil {
		if seleniumError, ok := err.(*selenium.Error); ok {
			webdriver.NewSessionAndRefresh(seleniumError.Message)
			responseText, _ := webdriver.WebDriver.ExecuteScriptAsync(script, nil)
			c.Writer.Write([]byte(responseText.(string)))
			return true
		}
	}

	return false
}

//goland:noinspection GoUnhandledErrorResult
func GetModels(c *gin.Context) {
	url := apiPrefix + "/models"
	accessToken := api.GetAccessToken(c.GetHeader(api.AuthorizationHeader))
	script := getGetScript(url, accessToken, getModelsErrorMessage)
	responseText, err := webdriver.WebDriver.ExecuteScriptAsync(script, nil)
	if handleSeleniumError(err, script, c) {
		return
	}

	if responseText == getModelsErrorMessage {
		tryToRefreshPage()
		c.JSON(http.StatusInternalServerError, api.ReturnMessage(getModelsErrorMessage))
		return
	}

	c.Writer.Write([]byte(responseText.(string)))
}

func DealFromAiFakeOpen(apiPath string, c *gin.Context) {
	proxyURL := "https://ai.fakeopen.com" + apiPath // 转发目标URL
	method := c.Request.Method                      // 获取请求方法
	var body io.Reader                              // 定义body
	if method == "POST" || method == "PUT" {        // 如果是POST或PUT请求，则读取请求Body
		body = c.Request.Body
	}

	req, err := http.NewRequest(method, proxyURL, body) // 创建转发请求
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	req.Header = c.Request.Header // 将原始请求的Header设置到转发请求上

	client := http.Client{}     // 创建HTTP Client
	resp, err := client.Do(req) // 发送转发请求
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	defer resp.Body.Close() // 关闭响应Body

	// 将转发请求得到的响应Header设置到原始响应中
	for k, v := range resp.Header {
		c.Header(k, v[0])
	}

	c.Status(resp.StatusCode)    // 设置响应状态码
	io.Copy(c.Writer, resp.Body) // 将转发响应的Body写入原始响应
}

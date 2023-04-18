package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/linweiyuan/go-chatgpt-api/api/chatgpt"
	"github.com/linweiyuan/go-chatgpt-api/api/official"
	"github.com/linweiyuan/go-chatgpt-api/middleware"
	"github.com/linweiyuan/go-chatgpt-api/webdriver"
)

func init() {
	gin.ForceConsoleColor()
}

func Recover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				webdriver.NewSessionAndRefresh("panic")
			}
		}()
		c.Next()
	}
}

func cors(c *gin.Context) {
	// 添加跨域响应头
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Methods", "*")
	c.Header("Access-Control-Allow-Headers", "*")
	c.Next()
}

func main() {
	router := gin.Default()
	router.Use(cors)
	router.Use(Recover())
	router.Use(middleware.HeaderCheckMiddleware())

	// chatgpt
	conversationsGroup := router.Group("/api/conversations")
	{
		conversationsGroup.OPTIONS("", chatgpt.GetConversations)

		// PATCH is official method, POST is added for Java support
		conversationsGroup.PATCH("", chatgpt.ClearConversations)
		conversationsGroup.POST("", chatgpt.ClearConversations)
	}

	conversationGroup := router.Group("/api/conversation")
	{
		conversationGroup.POST("", chatgpt.StartConversation)
		conversationGroup.POST("/gen_title/:id", chatgpt.GenerateTitle)
		conversationGroup.OPTIONS("/:id", chatgpt.GetConversation)

		// rename or delete conversation use a same API with different parameters
		conversationGroup.PATCH("/:id", chatgpt.UpdateConversation)
		conversationGroup.POST("/:id", chatgpt.UpdateConversation)

		conversationGroup.POST("/message_feedback", chatgpt.FeedbackMessage)
	}

	router.OPTIONS("/api/models", chatgpt.GetModels)

	router.OPTIONS("/api/conversation_limit", func(c *gin.Context) {
		chatgpt.GetApiData("conversation_limit", c)
	})

	// official api
	apiGroup := router.Group("/v1")
	{
		apiGroup.POST("/chat/completions", official.ChatCompletions)
	}
	router.GET("/dashboard/billing/credit_grants", official.CheckUsage)

	err := router.Run(":8080")
	if err != nil {
		log.Fatal("Failed to start server:" + err.Error())
	}
}

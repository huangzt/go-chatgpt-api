package main

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/linweiyuan/go-chatgpt-api/api/chatgpt"
	"github.com/linweiyuan/go-chatgpt-api/api/official"
	"github.com/linweiyuan/go-chatgpt-api/middleware"
	"github.com/linweiyuan/go-chatgpt-api/webdriver"
	"log"
	"net/http"
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

func main() {
	router := gin.Default()

	// 设置CORS中间件，允许所有来源和所有请求头，并允许预检请求响应
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowHeaders = []string{"*"} // 允许所有请求头
	config.AllowMethods = []string{"*"}
	router.Use(cors.New(config))

	//router.Use(Recover())
	//router.Use(middleware.HeaderCheckMiddleware())

	// chatgpt
	conversationsGroup := router.Group("/api/conversations")
	{
		conversationsGroup.Use(Recover())
		conversationsGroup.Use(middleware.HeaderCheckMiddleware())

		conversationsGroup.GET("", chatgpt.GetConversations)

		// PATCH is official method, POST is added for Java support
		conversationsGroup.PATCH("", chatgpt.ClearConversations)
		conversationsGroup.POST("", chatgpt.ClearConversations)
	}

	conversationGroup := router.Group("/api/conversation")
	{
		conversationGroup.Use(Recover())
		conversationGroup.Use(middleware.HeaderCheckMiddleware())

		conversationGroup.POST("", chatgpt.StartConversation)

		conversationGroup.POST("/gen_title/:id", chatgpt.GenerateTitle)

		conversationGroup.GET("/:id", chatgpt.GetConversation)

		// rename or delete conversation use a same API with different parameters
		conversationGroup.PATCH("/:id", chatgpt.UpdateConversation)
		conversationGroup.POST("/:id", chatgpt.UpdateConversation)

		conversationGroup.POST("/message_feedback", chatgpt.FeedbackMessage)
	}

	modelsGroup := router.Group("/api/models")
	{
		modelsGroup.Use(Recover())
		modelsGroup.Use(middleware.HeaderCheckMiddleware())

		modelsGroup.GET("", chatgpt.GetModels)
	}

	// 没有的api从大老那里拿
	router.GET("/api/conversation_limit", func(c *gin.Context) {
		chatgpt.DealFromAiFakeOpen("/api/conversation_limit", c)
	})

	router.GET("/auth", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "https://ai.fakeopen.com/auth")
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

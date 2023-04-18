package main

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/linweiyuan/go-chatgpt-api/api/chatgpt"
	"github.com/linweiyuan/go-chatgpt-api/api/official"
	"github.com/linweiyuan/go-chatgpt-api/middleware"
	"github.com/linweiyuan/go-chatgpt-api/webdriver"
	"log"
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

	router.Use(Recover())
	router.Use(middleware.HeaderCheckMiddleware())

	// chatgpt
	conversationsGroup := router.Group("/api/conversations")
	{
		conversationsGroup.GET("", chatgpt.GetConversations)

		// PATCH is official method, POST is added for Java support
		conversationsGroup.PATCH("", chatgpt.ClearConversations)
		conversationsGroup.POST("", chatgpt.ClearConversations)
	}

	conversationGroup := router.Group("/api/conversation")
	{
		conversationGroup.POST("", chatgpt.StartConversation)

		conversationGroup.POST("/gen_title/:id", chatgpt.GenerateTitle)

		conversationGroup.GET("/:id", chatgpt.GetConversation)

		// rename or delete conversation use a same API with different parameters
		conversationGroup.PATCH("/:id", chatgpt.UpdateConversation)
		conversationGroup.POST("/:id", chatgpt.UpdateConversation)

		conversationGroup.POST("/message_feedback", chatgpt.FeedbackMessage)
	}

	router.GET("/api/models", chatgpt.GetModels)

	router.GET("/api/conversation_limit", func(c *gin.Context) {
		//chatgpt.GetApiData("conversation_limit", c)
		c.Writer.Write([]byte("{\"message_cap\":25,\"message_cap_window\":180,\"message_disclaimer\":{\"textarea\":\"GPT-4 currently has a cap of 25 messages every 3 hours.\",\"model-switcher\":\"You've reached the GPT-4 cap, which gives all ChatGPT Plus users a chance to try the model.\\n\\nPlease check back soon.\"}}"))
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

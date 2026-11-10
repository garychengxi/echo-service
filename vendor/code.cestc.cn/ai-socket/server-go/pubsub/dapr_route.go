package pubsub

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	dapr "github.com/dapr/go-sdk/client"
	daprc "github.com/dapr/go-sdk/service/common"
)

const (
	MetadataRawPayload = "rawPayload"
	PubSubKey          = "pubsub.Subscription"
)

var (
	TopicAntiPattern = strings.NewReplacer(
		"/", "__",
		":", "_",
		"?", "_",
	)

	subscriptions = make([]Subscription, 0)
)

type Subscription = daprc.Subscription

type ConfigHandler func(*Subscription) *Subscription

// ValidTopicName returns a valid name in case input is a path
func ValidTopicName(topic string) string {
	t := strings.TrimPrefix(topic, "/")
	return TopicAntiPattern.Replace(t)
}

// Subscribe returns core.Engine.Subscribe http handlers to dapr.
func Subscribe(engine *gin.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, subscriptions)
	}
}

// OnEvent subscribe topic on relativePath.
//
// Example:
//
//	pubsub.OnEvent(engine, "path_of_api", nil, ReceiveEvent)
func OnEvent(engine *gin.Engine, relativePath string, configHandler ConfigHandler, handlers ...gin.HandlerFunc) {
	topic := ValidTopicName(relativePath)
	route := strings.TrimPrefix(engine.BasePath(), "/") + "/" + strings.TrimPrefix(relativePath, "/")
	subscription := &Subscription{
		PubsubName: topic,
		Topic:      topic,
		Route:      route,
	}
	if configHandler != nil {
		subscription = configHandler(subscription)
	}
	subscriptions = append(subscriptions, *subscription)

	engine.POST(relativePath, handlers...)
}

// OnEventWithGroup subscribe topic on group/relativePath.
//
// Unable to add Subscribe to existing RouteGroup
// because it's used as return value of interface that Engine and RouteGroup implemented.
//
// Example:
//
//	g := engine.Group("/v1/datasets")
//	g.POST("", Create)
//	g.GET("", List)
//	engine.OnEventWithGroup(g, "path_of_api", nil, ReceiveEvent)
func OnEventWithGroup(group *gin.RouterGroup, relativePath string, configHandler ConfigHandler, handlers ...gin.HandlerFunc) {
	topic := ValidTopicName(relativePath)
	route := strings.TrimPrefix(group.BasePath(), "/") + "/" + strings.TrimPrefix(relativePath, "/")
	subscription := &Subscription{
		PubsubName: topic,
		Topic:      topic,
		Route:      route,
	}
	if configHandler != nil {
		subscription = configHandler(subscription)
	}
	subscriptions = append(subscriptions, *subscription)

	group.POST(relativePath, handlers...)
}

func PublishEvent(ctx *gin.Context, pubsubName string, topic string, data any) error {
	client, err := dapr.NewClient()
	if err != nil {
		return err
	}

	err = client.PublishEvent(ctx, pubsubName, pubsubName, data)
	return err
}

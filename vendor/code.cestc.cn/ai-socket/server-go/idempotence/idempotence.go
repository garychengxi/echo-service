package idempotence

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"sync"
	"time"

	"code.cestc.cn/ai-socket/server-go/conf"
	core "code.cestc.cn/ai-socket/server-go/pkg"
)

type IdempotentItem struct {
	RequestURL  string
	RequestBody *bytes.Buffer
	StatueCode  int
	ResponseBuf *bytes.Buffer
	MD5         string
}

func CalculateIdempotenceMD5(requestURL string, requestBody []byte) string {
	h := md5.New()
	h.Write([]byte(requestURL))
	h.Write(requestBody)
	return hex.EncodeToString(h.Sum(nil))
}

type IdempotenceController struct {
	store core.KVStore
}

var idempotenceController *IdempotenceController
var once sync.Once

func GetIdempotenceController() *IdempotenceController {
	once.Do(func() {
		ttl := conf.Default().HTTPServer.IdempotenceTTL
		if ttl <= 0 {
			ttl = 24 * 60 // 单位:分钟
		}
		ttlDuration := time.Duration(ttl) * time.Minute
		idempotenceController = &IdempotenceController{
			store: core.NewInMemoryKVStore(ttlDuration),
		}
	})
	return idempotenceController
}

func (c *IdempotenceController) GetIdempotentItem(clientToken string) *IdempotentItem {
	var item *IdempotentItem
	if data := c.store.Get(clientToken); data != nil {
		item, _ = data.(*IdempotentItem)
	}
	return item
}

func (c *IdempotenceController) SetIdempotentItem(clientToken string, item *IdempotentItem) {
	c.store.Set(clientToken, item)
}

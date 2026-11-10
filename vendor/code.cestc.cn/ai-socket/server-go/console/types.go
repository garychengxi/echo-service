package console

import (
	"code.cestc.cn/ai-socket/server-go/errors"
)

type Response struct {
	*errors.Error `json:",inline,omitempty"`
	Data          any `json:"data,omitempty"`
}

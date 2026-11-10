package binder

import (
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"

	"code.cestc.cn/ai-socket/server-go/errors"
)

type HTTPBinder struct {
	req     interface{}
	context *gin.Context
	errs    []error
}

func (b *HTTPBinder) Error() error {
	if len(b.errs) == 0 {
		return nil
	}
	return combineErrors(b.errs)
}

func (b *HTTPBinder) All() *HTTPBinder {
	if b.context.Request.ContentLength > 0 {
		switch b.context.ContentType() {
		case binding.MIMEMultipartPOSTForm:
			b.MultiForm()
		default:
			b.JSON()
		}
	}

	return b.Query().Uri()
}

func (b *HTTPBinder) Header() *HTTPBinder {
	err := b.context.ShouldBindHeader(b.req)
	if err != nil {
		b.errs = append(b.errs, err)
	}
	return b
}

// nolint
func (b *HTTPBinder) Uri() *HTTPBinder {
	err := b.context.ShouldBindUri(b.req)
	if err != nil {
		b.errs = append(b.errs, err)
	}
	return b
}

func (b *HTTPBinder) Form() *HTTPBinder {
	err := b.context.ShouldBindWith(b.req, binding.Form)
	if err != nil {
		b.errs = append(b.errs, err)
	}
	return b
}

func (b *HTTPBinder) MultiForm() *HTTPBinder {
	err := b.context.ShouldBindWith(b.req, binding.FormMultipart)
	if err != nil {
		b.errs = append(b.errs, err)
	}
	return b
}

func (b *HTTPBinder) Query() *HTTPBinder {
	err := b.context.ShouldBindQuery(b.req)
	if err != nil {
		b.errs = append(b.errs, err)
	}
	return b
}

func (b *HTTPBinder) JSON() *HTTPBinder {
	err := b.context.ShouldBindJSON(b.req)
	if err != nil {
		b.errs = append(b.errs, err)
	}
	return b
}

func Bind(c *gin.Context, req interface{}) *HTTPBinder {
	return &HTTPBinder{
		context: c,
		req:     req,
		errs:    make([]error, 0),
	}
}

func combineErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}

	messageSet := make(map[string]struct{})
	for _, err := range errs {
		if err == nil {
			continue
		}

		switch e := err.(type) {
		case validator.ValidationErrors:
			for _, fe := range e {
				messageSet[fieldError(fe)] = struct{}{}
			}
		default:
			messageSet[err.Error()] = struct{}{}
		}
	}

	var messages string
	for m := range messageSet {
		if messages != "" {
			messages += "\n"
		}
		messages += m
	}

	return errors.NewInvalidParameter(messages)
}

func fieldError(fe validator.FieldError) string {
	if message := translate(fe.Tag()); message != "" {
		return message
	}
	return fe.Error()
}

func translate(tag string) string {
	return map[string]string{
		tagLocalname: tagLocalname + " should match regular expression " + LocalnamePattern,
		tagOrderby:   tagOrderby + " contains invalid character",
	}[tag]
}

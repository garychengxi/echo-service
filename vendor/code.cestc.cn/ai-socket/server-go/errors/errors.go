package errors

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"

	"code.cestc.cn/ai-socket/logger-go"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
	"gorm.io/gorm"

	"code.cestc.cn/ai-socket/server-go/conf"
	"code.cestc.cn/ai-socket/server-go/constants"
	"code.cestc.cn/ai-socket/server-go/pkg/utils"
)

var (
	ErrorNotImplemented              = NewError(http.StatusInternalServerError, "NotImplemented", "API 未实现")       // API NotImplemented.
	ErrorAccessDenied                = NewError(http.StatusForbidden, "AccessDenied", "拒绝访问")                      // Access denied.
	ErrorInappropriateJSON           = NewError(http.StatusBadRequest, "InappropriateJSON", "json 格式错误")           // The JSON you provided was well-formed and valid, but not appropriate for this operation.
	ErrorInternalError               = NewError(http.StatusInternalServerError, "InternalError", "服务器内部错误，请稍后重试！") // We encountered an internal error. Please try again.
	ErrorInvalidHTTPRequest          = NewError(http.StatusBadRequest, "InvalidHTTPRequest", "HTTP 请求参数错误")        // There was an error in the body of your HTTP request.
	ErrorInvalidURI                  = NewError(http.StatusBadRequest, "InvalidURI", "无效 URI")                     // Could not parse the specified URI.
	ErrorMalformedJSON               = NewError(http.StatusBadRequest, "MalformedJSON", "无效 json")                 // The JSON you provided was not well-formed.
	ErrorInvalidVersion              = NewError(http.StatusNotFound, "InvalidVersion", "无效 API 版本")                //The API version specified was invalid.
	ErrorOptInRequired               = NewError(http.StatusForbidden, "OptInRequired", "需要订阅服务")                   // A subscription for the service is required.
	ErrorRequestExpired              = NewError(http.StatusBadRequest, "RequestExpired", "请求已过期")                  // Request has expired.
	ErrorIdempotentParameterMismatch = NewError(http.StatusForbidden, "IdempotentParameterMismatch", "幂等参数不匹配")    // The request uses the same client token as a previous, but non-identical request.
	ErrorSignatureDoesNotMatch       = NewError(http.StatusBadRequest, "SignatureDoesNotMatch", "签名不匹配")           // The request signature we calculated does not match the signature you provided. Check your Secret Access Key and signing method. Consult the service documentation for details.
	ErrorResourceNotFound            = NewError(http.StatusNotFound, "ResourceNotFound", "资源不存在")                  // The resource was not found.
	ErrorResourceIsExists            = NewError(http.StatusConflict, "ResourceIsExists", "资源已存在")
	ErrorIncorrectString             = NewError(http.StatusBadRequest, "IncorrectString", "字符串值不正确")
	ErrorAPINotFound                 = NewError(http.StatusNotFound, "APINotFound", "API 不存在") // The API was not found.
)

var dbOrderErrMsgRegex = regexp.MustCompile("^Unknown column '(?P<column>.+?)' in 'order clause'$")

var _ error = (*Error)(nil)

// Error 公共错误结构
type Error struct {
	// Code 错误码
	Code string `json:"code"`

	// Message 详细错误信息
	Message string `json:"message,omitempty"`

	// RequestID 请求id，接收到请求的时候由中间件自动注入
	RequestID string `json:"requestId"`

	// StatusCode 状态码，推荐6位数字
	StatusCode int `json:"statusCode"`

	// Status HTTP状态码，JSON中忽略
	Status int `json:"-"`
}

func (e *Error) Error() string {
	// 只返回错误信息，不返回错误码和请求ID
	return e.Message
}

func (e *Error) SetStatusCode() {
	if conf.Default() == nil {
		e.StatusCode = e.Status
		return
	}
	if e.StatusCode != 0 {
		return
	}
	e.StatusCode = utils.JoinInt(conf.Default().AppCode, e.Status)
}

func NewError(status int, code string, message string) error {
	err := &Error{
		Status:  status,
		Code:    code,
		Message: message,
	}
	return err
}

func NewEmptyParameter(parameterName string) error {
	return &Error{
		Status:  http.StatusBadRequest,
		Code:    "InvalidParameter",
		Message: "Parameter " + parameterName + " is empty",
	}
}

func NewInvalidParameter(message string) error {
	return &Error{
		Status:  http.StatusBadRequest,
		Code:    "InvalidParameter",
		Message: message,
	}
}

func NewDBError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrorResourceNotFound
	}

	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return ErrorResourceIsExists
	}
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1366 {
		return ErrorIncorrectString
	}

	if strings.HasPrefix(err.Error(), "UNIQUE constraint failed") {
		return ErrorResourceIsExists
	}

	// unknown column in order clause
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1054 {
		m := dbOrderErrMsgRegex.FindStringSubmatch(mysqlErr.Message)
		if len(m) > dbOrderErrMsgRegex.NumSubexp() {
			return NewInvalidParameter("Unknown field used in orderBy")
		}
	}

	// mongodb error
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrorResourceNotFound
	}
	if mongo.IsDuplicateKeyError(err) {
		return ErrorResourceIsExists
	}

	return NewError(http.StatusInternalServerError, "InternalError", err.Error())
}

func New(err error) *Error {
	if err == nil {
		return nil
	}

	switch e := err.(type) {
	case *Error:
		message := e.Message
		if e.Status == http.StatusInternalServerError {
			message = ErrorInternalError.Error()
			if gin.Mode() == gin.DebugMode {
				message = err.Error()
			}
		}
		return &Error{
			Status:    e.Status,
			Code:      e.Code,
			Message:   message,
			RequestID: e.RequestID,
		}
	default:
		message := ErrorInternalError.Error()
		if gin.Mode() == gin.DebugMode {
			message = err.Error()
		}
		newErr := Error{
			Status:  http.StatusInternalServerError,
			Code:    "InternalError",
			Message: message,
		}
		return &newErr
	}
}

// IsStatusCode check error's http status code equal to targetCode
func IsStatusCode(err error, targetCode int) bool {
	switch e := err.(type) {
	case *Error:
		return e.Status == targetCode
	}
	return false
}

func IsCode(err error, code string) bool {
	switch e := err.(type) {
	case *Error:
		return e.Code == code
	}
	return false
}

// Wrap returns a new error with wrap message
func Wrap(err error, message string) error {
	switch e := err.(type) {
	case *Error:
		newError := &Error{
			Code:       e.Code,
			Message:    message + " " + e.Message,
			RequestID:  e.RequestID,
			StatusCode: e.StatusCode,
			Status:     e.Status,
		}
		return newError
	}
	return errors.Wrap(err, message)
}

// WithMessage returns a new error with new message
func WithMessage(err error, message string) error {
	switch e := err.(type) {
	case *Error:
		newError := &Error{
			Code:       e.Code,
			Message:    message,
			RequestID:  e.RequestID,
			StatusCode: e.StatusCode,
			Status:     e.Status,
		}
		return newError
	}
	return errors.WithMessage(err, message)
}

// ErrorHandler 统一拦截异常，按照规范返回错误
// 如果启用了 AutoHandleAPIError，会自动处理 /{appName}/api/* 接口的响应状态码
func ErrorHandler(config *conf.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查是否需要自动处理 API 错误（将非200状态码改为200）
		shouldAutoHandle := config != nil && config.HTTPServer.AltoHandlerAPIError && !config.HTTPServer.ConsoleEnable

		// 如果 shouldAutoHandle = false，则不进行自动处理，直接返回错误
		if !shouldAutoHandle {
			c.Next()

			lastErr := c.Errors.Last()
			if lastErr == nil {
				return
			}

			AbortWithError(c, lastErr.Err)
			return
		}
		var writer *statusCodeWriter
		// 检查路径是否为 /{appName}/api/*
		path := c.Request.URL.Path
		appName := config.AppName
		apiPrefix := "/" + appName + "/api"
		// 检查是否需要自动处理 API 错误（将非200状态码改为200）
		allowOriginsExist := config != nil && len(config.HTTPServer.AllowAPIPathNoHandlerAPIError) > 0
		if allowOriginsExist {
			for _, allowOrigin := range config.HTTPServer.AllowAPIPathNoHandlerAPIError {
				if strings.HasPrefix(path, allowOrigin) {
					c.Next()
					lastErr := c.Errors.Last()
					if lastErr == nil {
						return
					}
					AbortWithError(c, lastErr.Err)
					return
				}
			}
		}
		// 匹配 /{appName}/api 或 /{appName}/api/*
		if path == apiPrefix || strings.HasPrefix(path, apiPrefix+"/") {
			// 使用自定义的 ResponseWriter 来拦截状态码和响应体
			writer = &statusCodeWriter{
				ResponseWriter: c.Writer,
				statusCode:     http.StatusOK,
				written:        false,
				body:           &bytes.Buffer{},
			}
			c.Writer = writer
		}

		c.Next()

		lastErr := c.Errors.Last()
		if lastErr == nil {
			return
		}

		// 如果启用了自动处理且路径匹配，需要特殊处理错误响应
		if writer != nil {
			// 规范化错误并设置 RequestID
			errObj := New(lastErr.Err)
			errObj.RequestID = c.GetHeader(constants.HeaderRequestID)
			// 确保 StatusCode 已设置（如果为 0，则调用 SetStatusCode 设置）
			if errObj.StatusCode == 0 {
				errObj.SetStatusCode()
			}
			// 如果还没有写入响应，直接返回 JSON，状态码为200
			if !writer.written {
				logger.Errorf(c, "API Error: %+v", lastErr.Err)
				c.JSON(http.StatusOK, errObj)
				return
			}
			// 如果已经写入了响应，需要清空并重新写入
			// 但根据业务逻辑，通常错误处理会在写入响应之前完成
			// 如果已经写入，说明可能不是错误响应，直接返回
			return
		}

		AbortWithError(c, lastErr.Err)
	}
}

// statusCodeWriter 自定义 ResponseWriter，用于拦截状态码和响应体
type statusCodeWriter struct {
	gin.ResponseWriter
	statusCode int
	written    bool
	body       *bytes.Buffer
}

func (w *statusCodeWriter) WriteHeader(code int) {
	w.statusCode = code
	// 只有当状态码是200时才立即写入，否则先记录状态码，稍后统一改为200
	if code == http.StatusOK {
		w.written = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *statusCodeWriter) Write(data []byte) (int, error) {
	// 缓存响应体
	w.body.Write(data)
	// 如果状态码还没有写入，先写入200
	if !w.written {
		if w.statusCode != http.StatusOK {
			w.statusCode = http.StatusOK
		}
		w.written = true
		w.ResponseWriter.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusCodeWriter) WriteString(s string) (int, error) {
	// 如果状态码还没有写入，先写入200
	if !w.written {
		if w.statusCode != http.StatusOK {
			w.statusCode = http.StatusOK
		}
		w.written = true
		w.ResponseWriter.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.WriteString(s)
}

func (w *statusCodeWriter) Status() int {
	// 返回200，因为我们要将所有状态码都改为200
	return http.StatusOK
}

func AbortWithError(c *gin.Context, err error) {
	logger.Errorf(c, "API Error: %+v", err)
	wrong := New(err)
	wrong.RequestID = c.GetHeader(constants.HeaderRequestID)
	c.AbortWithStatusJSON(wrong.Status, wrong)
}

func NewResourceIsExists(message string) error {
	return &Error{
		Status:  http.StatusConflict,
		Code:    "ResourceIsExists",
		Message: message,
	}
}

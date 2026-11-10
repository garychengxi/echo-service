package sdk

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

var (
	re = regexp.MustCompile("[^a-zA-Z0-9_-]")
)

const (
	OrderASC  = "asc"
	OrderDESC = "desc"
)

const (
	minPageNo       = 1
	maxPageSize     = 200
	defaultPageSize = 20
)

// swagger:model PagingResponse
type PagingResponse struct {
	Results    interface{} `json:"result"`
	TotalCount int         `json:"totalCount"`
}

func NewPagingRequest() PagingRequest {
	return PagingRequest{
		PageNo:   minPageNo,
		PageSize: defaultPageSize,
	}
}

// PagingRequest 通用分页参数
// swagger:model PagingRequest
type PagingRequest struct {
	// 页码
	//
	// Example: 1
	PageNo int `json:"pageNo"`
	// 每页的大小
	//
	// Example: 10
	PageSize int `json:"pageSize" binding:"max=200"`
	// 排序规则: asc: 升序, desc: 倒序, 默认倒序
	//
	// Example: desc
	Order string `json:"order" binding:"omitempty,oneof=asc desc"`
	// 排序依据
	//
	// Example: name
	OrderBy string `json:"orderBy" binding:"orderby"`
	// mongo 排序规则
	//
	// Example: []string{ "{order: desc}", "{id:asc}" }
	Orders []string `json:"orders" example:"[{create_ad:desc},{id:asc}]"`
}

func (r PagingRequest) IsPage() bool {
	return r.PageNo != 0 && r.PageSize != 0
}

func (r PagingRequest) GetPageNo() int {
	if r.PageNo == 0 {
		return minPageNo
	}
	return r.PageNo
}

func (r PagingRequest) GetPageSize() int {
	if r.PageSize == 0 {
		return defaultPageSize
	}
	if r.PageSize > maxPageSize {
		return maxPageSize
	}
	return r.PageSize
}

func (r PagingRequest) Offset() int {
	return (r.GetPageNo() - 1) * r.GetPageSize()
}

func (r PagingRequest) Limit() int {
	return r.GetPageSize()
}

func (r PagingRequest) IsOrder() bool {
	return r.OrderBy != ""
}

func (r PagingRequest) GetOrder() string {
	if r.Order == OrderASC {
		return OrderASC
	}
	return OrderDESC
}

// DBOrder 数据库排序语句
func (r PagingRequest) DBOrder() string {
	if !r.IsOrder() {
		return fmt.Sprintf("created_at %s", r.GetOrder())
	}
	r.OrderBy = re.ReplaceAllString(r.OrderBy, "")
	r.OrderBy = camelToSnake(r.OrderBy)
	return fmt.Sprintf("%s %s", r.OrderBy, r.GetOrder())
}

// Paging 对数组做分页处理
func Paging(arr interface{}, pageNo int, pageSize int) []interface{} {
	offset := (pageNo - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	limit := offset + pageSize
	if limit < 0 {
		limit = 0
	}
	if limit > reflect.ValueOf(arr).Len() {
		limit = reflect.ValueOf(arr).Len()
	}

	paged := make([]interface{}, 0)
	for i := offset; i < limit; i++ {
		paged = append(paged, reflect.ValueOf(arr).Index(i).Interface())
	}
	return paged
}

// 将 api 中的驼峰字段转化为 db 下划线字段形式
func camelToSnake(s string) string {
	// 匹配所有大写字母前插入下划线
	matchFirstCap := regexp.MustCompile("(.)([A-Z][a-z]+)")
	matchAllCap := regexp.MustCompile("([a-z0-9])([A-Z])")

	snake := matchFirstCap.ReplaceAllString(s, "${1}_${2}")
	snake = matchAllCap.ReplaceAllString(snake, "${1}_${2}")
	return strings.ToLower(snake)
}

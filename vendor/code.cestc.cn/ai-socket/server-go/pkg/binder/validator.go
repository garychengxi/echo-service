package binder

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

const (
	TagKeyPattern   = "^.{0,64}$"
	TagValuePattern = "^.{0,255}$"

	CategoryPattern              = "^[a-zA-Z0-9-_/\\.\\*\\p{Han}]{0,105}$"
	LocalnamePattern             = "^(?:[a-zA-Z0-9](?:[a-zA-Z0-9_-]{0,34}[a-zA-Z0-9])?)?$"
	AlphanumericPattern          = "^[0-9a-zA-Z]{0,36}$"
	LowercaseAlphanumericPattern = "^[0-9a-z]{0,36}$"

	tagLowercaseAlphanumeric = "lowercase_alphanumeric"
	tagCategory              = "category"
	tagCategories            = "categories"
	tagAlphanumeric          = "alphanumeric"
	tagLocalname             = "localname"
	tagOrderby               = "orderby"
	tagTags                  = "tags"
)

func name(fl validator.FieldLevel) bool {
	v, ok := fl.Field().Interface().(string)
	if !ok {
		// name字段必须为string类型
		return false
	}

	if v == "" {
		// name字段可为空
		return true
	}

	success, _ := regexp.MatchString("^[0-9a-zA-Z_]{1,20}$", v)
	return success
}

func lowercaseAlphanumeric(fl validator.FieldLevel) bool {
	v, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}

	success, err := regexp.MatchString(LowercaseAlphanumericPattern, v)
	return err == nil && success
}

func alphanumeric(fl validator.FieldLevel) bool {
	v, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}

	success, err := regexp.MatchString(AlphanumericPattern, v)
	return err == nil && success
}

func localname(fl validator.FieldLevel) bool {
	v, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}

	success, err := regexp.MatchString(LocalnamePattern, v)
	return err == nil && success
}

func orderby(fl validator.FieldLevel) bool {
	v, ok := fl.Field().Interface().(string)
	return ok && !strings.Contains(v, " ")
}

func id(fl validator.FieldLevel) bool {
	v, ok := fl.Field().Interface().(string)
	if !ok {
		// id字段必须为string类型
		return false
	}

	if v == "" {
		// id字段可为空
		return true
	}

	// 增加前缀判断
	prefix := fl.Param()
	if !strings.HasPrefix(v, prefix) {
		return false
	}

	success, _ := regexp.MatchString("^[a-z0-9-]{9,12}$", v)
	return success
}

func category(fl validator.FieldLevel) bool {
	v, ok := fl.Field().Interface().(string)
	if !ok {
		return false
	}

	success, err := regexp.MatchString(CategoryPattern, v)
	return err == nil && success
}

func categories(fl validator.FieldLevel) bool {
	v, ok := fl.Field().Interface().([]string)
	if !ok {
		return false
	}

	for _, s := range v {
		success, err := regexp.MatchString(CategoryPattern, s)
		if err != nil || !success {
			return false
		}
	}

	return true
}

func tag(fl validator.FieldLevel) bool {
	var tags map[string][]string

	switch v := fl.Field().Interface().(type) {
	case map[string][]string:
		tags = v
	case map[string]string:
		tags = make(map[string][]string, len(v))
		for k, val := range v {
			tags[k] = []string{val}
		}
	default:
		return false
	}

	for k, vs := range tags {
		success, err := regexp.MatchString(TagKeyPattern, k)
		if err != nil || !success {
			return false
		}
		for _, v := range vs {
			success, err = regexp.MatchString(TagValuePattern, v)
			if err != nil || !success {
				return false
			}
		}
	}
	return true
}

func RegisterValidator() {
	v, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return
	}
	_ = v.RegisterValidation("name", name)
	_ = v.RegisterValidation(tagLocalname, localname)
	_ = v.RegisterValidation(tagOrderby, orderby)
	_ = v.RegisterValidation("id", id)
	_ = v.RegisterValidation(tagAlphanumeric, alphanumeric)
	_ = v.RegisterValidation(tagCategory, category)
	_ = v.RegisterValidation(tagCategories, categories)
	_ = v.RegisterValidation(tagLowercaseAlphanumeric, lowercaseAlphanumeric)
	_ = v.RegisterValidation(tagTags, tag)
}

package core

import (
	"fmt"
	"reflect"

	"github.com/mitchellh/mapstructure"
)

const (
	RenderTypeJSON RenderType = iota
	RenderTypeTable
	RenderTypeCSV
	RenderTypeHTML
	RenderTypeMarkdown
)

type RenderType int

func JSONToMap(obj interface{}) map[string]interface{} {
	var result map[string]interface{}
	_ = StructDecode(obj, &result, "json")
	return result
}

func FormToStringMap(obj interface{}) map[string]string {
	var result map[string]interface{}
	_ = StructDecode(obj, &result, "form")

	stringMap := make(map[string]string, len(result))
	for k, v := range result {
		if v != nil {
			refVal := reflect.ValueOf(v)
			if refVal.Kind() == reflect.Ptr {
				if refVal.IsNil() {
					continue
				}

				va := reflect.Indirect(reflect.ValueOf(v))
				reflect.ValueOf(v).Elem().Set(va)
				stringMap[k] = fmt.Sprint(va)
			} else {
				stringMap[k] = fmt.Sprint(v)
			}

		}
	}
	return stringMap
}

func StructDecode(in interface{}, out interface{}, tag string) error {
	cfg := mapstructure.DecoderConfig{
		TagName: tag,
		Result:  out,
	}

	decoder, err := mapstructure.NewDecoder(&cfg)
	if err != nil {
		return err
	}

	return decoder.Decode(in)
}

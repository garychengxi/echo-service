package core

import (
	"encoding/json"
	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
)

const (
	fileExtJSON = ".json"
	fileExtTOML = ".toml"
	fileExtYAML = ".yaml"
	fileExtYML  = ".yml"
)

// ParseFile 解析文件，将文件内容转为结构体，支持toml, yaml, json格式
func ParseFile(filePath string, obj interface{}) error {
	buf, err := ioutil.ReadFile(filePath)
	if err != nil {
		return err
	}

	ext := path.Ext(filePath)
	switch ext {
	case fileExtJSON:
		err = json.Unmarshal(buf, obj)
	case fileExtTOML:
		err = toml.Unmarshal(buf, obj)
	case fileExtYAML, fileExtYML:
		err = yaml.Unmarshal(buf, obj)
	}

	return err
}

func CreateFileIfNotExist(dir string, fileName string) error {
	_, err := os.Stat(dir)
	if err == nil {
		// exist
		return nil
	}

	if os.IsExist(err) {
		return err
	}

	err = os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		return err
	}

	_, err = os.Create(filepath.Join(dir, fileName))
	return err
}

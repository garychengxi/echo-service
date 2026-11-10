package version

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-resty/resty/v2"
)

var (
	AppVersion    = "unknown"
	GitBranch     = "unknown"
	GitCommit     = "unknown"
	BuildDate     = "unknown"
	BuildPipeline = "unknown"
	BuildNumber   = "unknown"
)

type VersionInfo struct {
	AppVersion    string
	GitBranch     string
	GitCommit     string
	BuildDate     string
	BuildPipeline string
	BuildNumber   string
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, GetVersion())
	}
}

func GetVersionString() string {
	bytes, _ := json.Marshal(GetVersion())
	return string(bytes)
}

func GetVersion() *VersionInfo {
	versionInfo := VersionInfo{
		AppVersion:    AppVersion,
		GitBranch:     GitBranch,
		GitCommit:     GitCommit,
		BuildDate:     BuildDate,
		BuildPipeline: BuildPipeline,
		BuildNumber:   BuildNumber,
	}
	return &versionInfo
}

func GetVersionByURL(endpoint string) (*VersionInfo, error) {
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	var v VersionInfo
	url := endpoint + "/version"
	client := resty.New()
	_, err := client.R().
		SetResult(&v).
		Get(url)
	if err != nil {
		return nil, err
	}

	return &v, nil
}

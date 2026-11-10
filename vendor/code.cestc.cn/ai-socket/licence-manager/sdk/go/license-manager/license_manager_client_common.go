package license_manager

import (
	"log"
	"time"
)

const (
	maxRetries    = 5
	retryInterval = 1 * time.Second
)

// CheckLicence 检查 License 状态（带重试和endpoint轮询）
func (c *Client) CheckLicence(req *CheckLicenseRequest) bool {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		resp, err := c.doCheckLicence(req)
		if err == nil {
			log.Printf("CheckLicence result:%t", resp.Data.Result)
			return resp.StatusCode == 0
		}

		lastErr = err
		// 切换到下一个endpoint重试
		c.nextEndpoint()

		time.Sleep(retryInterval)
	}

	log.Printf("CheckLicence failed after %d err: %s \n", maxRetries, lastErr.Error())

	return false
}

func (c *Client) doCheckLicence(req *CheckLicenseRequest) (*CheckLicenseResponse, error) {
	var resp CheckLicenseResponse
	var sErr error

	url := c.getBaseURL() + "/licence-manager/api/v1/CheckLicence"
	result, err := c.R().SetBody(req).SetResult(&resp).SetError(&sErr).Post(url)
	if err != nil {
		return nil, err
	}
	if result.IsError() {
		return nil, sErr
	}
	return &resp, nil
}

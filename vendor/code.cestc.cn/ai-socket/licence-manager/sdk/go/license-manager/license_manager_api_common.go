package license_manager

type CheckLicenseRequest struct{}

type CheckLicenseResponse struct {
	Code       string `json:"code"`
	RequestId  string `json:"requestId"`
	Message    string `json:"message"`
	StatusCode int    `json:"statusCode"`
	Data       struct {
		Result    bool `json:"result"`
		Timestamp int  `json:"timestamp"`
	} `json:"data"`
}

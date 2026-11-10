package constants

// http header keys
const (
	HeaderRequestID     = "Request-ID"
	HeaderContentType   = "Content-Type"
	HeaderAuthorization = "Authorization"

	HeaderImpersonateOrgID        = "x-impersonate-target-org-id"
	HeaderImpersonateUserID       = "x-impersonate-target-user-id"
	HeaderImpersonateDepartmentID = "x-impersonate-target-department-id"

	HeaderOrgID  = "x-ceai-user-organization-id"
	HeaderUserID = "x-ceai-user-id"

	ContentTypeJSON = "application/json"
)

// http url query key
const (
	QueryOrgID       = "orgID"
	QueryUserID      = "userID"
	QueryClientToken = "clientToken"
)

// gin.Context keys
const (
	KeyRequestID = "RequestID"

	KeyOrgID    = "OrgID"    // auth后获得的OrgID，不可修改
	KeyUserID   = "UserID"   // auth后获得的UserID，不可修改
	KeyUserName = "UserName" // auth后获得的用户名称
	KeyUserRole = "UserRole" // 用户角色
	KeyAuthMode = "AuthMode" // 用户认证方式

	KeyImpersonateOriginOrgID  = "ImpersonateOriginOrgID"  // A impersonate成B访问服务时的orgID
	KeyImpersonateOriginUserID = "ImpersonateOriginUserID" // A impersonate成B访问服务时的userID
)

const (
	SizeMB = 1 * 1024 * 1024
)

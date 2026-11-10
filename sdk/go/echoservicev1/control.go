package echoservicev1

type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"displayName"`
	PlatformRole string `json:"platformRole"`
	Status       string `json:"status"`
}
type Container struct {
	ExecutionType  string   `json:"executionType"`
	WebURL         string   `json:"webUrl,omitempty"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	WorkspaceRoots []string `json:"workspaceRoots"`
	Endpoint       string   `json:"endpoint,omitempty"`
}
type Assignment struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	ContainerID string `json:"containerId"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}
type LoginRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	DeviceName string `json:"deviceName"`
}
type RefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}
type CreateUserRequest struct {
	Username     string `json:"username"`
	DisplayName  string `json:"displayName"`
	Password     string `json:"password"`
	PlatformRole string `json:"platformRole"`
}
type UpdateUserRequest struct {
	DisplayName  *string `json:"displayName"`
	PlatformRole *string `json:"platformRole"`
	Status       *string `json:"status"`
}
type CreateContainerRequest struct {
	WebURL         string   `json:"webUrl"`
	Name           string   `json:"name"`
	Endpoint       string   `json:"endpoint"`
	WorkspaceRoots []string `json:"workspaceRoots"`
}
type UpdateContainerRequest struct {
	WebURL *string `json:"webUrl"`
	Name   *string `json:"name"`
	Status *string `json:"status"`
}
type PasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

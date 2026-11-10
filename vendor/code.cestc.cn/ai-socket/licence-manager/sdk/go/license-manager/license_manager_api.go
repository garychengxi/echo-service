package license_manager

type LicenseManagerAPI interface {
	CheckLicence(*CheckLicenseRequest) bool
}

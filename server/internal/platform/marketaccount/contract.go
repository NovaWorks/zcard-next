// Package marketaccount defines the C0 marketplace account wire contract.
// It does not register HTTP routes, authenticate callers or change databases.
package marketaccount

import "time"

const (
	APIVersion           = "1"
	CustomerPrefix       = "/api/market/v1/customer"
	ViewPrefix           = "/api/market/v1/account-view"
	StorefrontPrefix     = "/api/market/v1/storefront"
	ChallengePath        = "/.well-known/zcard-market-site"
	SessionCookie        = "__Host-zcard-market-session"
	PreauthCookie        = "__Host-zcard-market-preauth"
	SessionLifetime      = 8 * time.Hour
	SessionIdle          = 30 * time.Minute
	ViewLifetime         = 15 * time.Minute
	PairLifetime         = 10 * time.Minute
	RegisterCodeLifetime = 10 * time.Minute
	ResetCodeLifetime    = 15 * time.Minute
	CodeCooldown         = time.Minute
	CodeMaxAttempts      = 5
	PollInterval         = 5 * time.Second
	MaxRequestBytes      = 16 << 10
	MaxResponseBytes     = 2 << 20
	MaxChallengeBytes    = 4 << 10
	DefaultPageSize      = 20
	MaxPageSize          = 100
)

type Empty struct{}
type Operation struct {
	RequestID string `json:"requestId"`
}
type PageQuery struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type ProductQuery struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Query  string `json:"q,omitempty"`
}
type PluginPath struct {
	PluginID string `json:"pluginId"`
}
type SitePath struct {
	SiteID string `json:"siteId"`
}
type Captcha struct {
	ID   string `json:"captchaId"`
	Code string `json:"captchaCode"`
}
type Bootstrap struct {
	CSRFToken      string `json:"csrfToken"`
	ExpiresAt      int64  `json:"expiresAt"`
	CaptchaEnabled bool   `json:"captchaEnabled"`
	CaptchaID      string `json:"captchaId"`
	CaptchaImage   string `json:"captchaImage"`
}
type CodeRequest struct {
	RequestID string `json:"requestId"`
	Email     string `json:"email"`
	Captcha
}
type CodeAccepted struct {
	ChallengeID       string `json:"challengeId"`
	RecoverySecret    string `json:"recoverySecret"`
	ExpiresAt         int64  `json:"expiresAt"`
	RetryAfterSeconds int    `json:"retryAfterSeconds"`
}
type Register struct {
	RequestID      string `json:"requestId"`
	ChallengeID    string `json:"challengeId"`
	RecoverySecret string `json:"recoverySecret"`
	Code           string `json:"code"`
	Username       string `json:"username"`
	Password       string `json:"password"`
}
type ResetPassword struct {
	RequestID      string `json:"requestId"`
	ChallengeID    string `json:"challengeId"`
	RecoverySecret string `json:"recoverySecret"`
	Code           string `json:"code"`
	NewPassword    string `json:"newPassword"`
}
type VerifyEmail struct {
	RequestID      string `json:"requestId"`
	ChallengeID    string `json:"challengeId"`
	RecoverySecret string `json:"recoverySecret"`
	Code           string `json:"code"`
}
type Login struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
	Captcha
}
type Customer struct {
	AccountID     string `json:"accountId"`
	DisplayName   string `json:"displayName"`
	Status        string `json:"status"`
	EmailMasked   string `json:"emailMasked"`
	EmailVerified bool   `json:"emailVerified"`
}
type Session struct {
	Customer  Customer `json:"customer"`
	ExpiresAt int64    `json:"expiresAt"`
	CSRFToken string   `json:"csrfToken"`
}
type Profile struct {
	Customer  Customer `json:"customer"`
	ExpiresAt int64    `json:"expiresAt"`
}
type Wallet struct {
	Balance  string `json:"balance"`
	Currency string `json:"currency"`
}

type Accepted struct {
	Accepted bool `json:"accepted"`
}
type ViewStart struct {
	RequestID   string `json:"requestId"`
	StartSecret string `json:"startSecret"`
	InstanceID  string `json:"instanceId"`
	ContextID   string `json:"contextId"`
}
type ViewPair struct {
	PairID       string `json:"pairId"`
	DeviceSecret string `json:"deviceSecret"`
	UserCode     string `json:"userCode"`
	ExpiresAt    int64  `json:"expiresAt"`
	PollSeconds  int    `json:"pollSeconds"`
}
type ViewProof struct {
	PairID       string `json:"pairId"`
	DeviceSecret string `json:"deviceSecret"`
}
type ViewConfirm struct {
	PairID       string `json:"pairId"`
	DeviceSecret string `json:"deviceSecret"`
	AccountID    string `json:"accountId"`
}
type ViewApproval struct {
	RequestID string `json:"requestId"`
	UserCode  string `json:"userCode"`
	Confirm   bool   `json:"confirm"`
}
type ViewLookup struct {
	UserCode string `json:"userCode"`
}
type ViewTarget struct {
	InstanceID string   `json:"instanceId"`
	Scopes     []string `json:"scopes"`
	ExpiresAt  int64    `json:"expiresAt"`
}
type ViewStatus struct {
	Status    string    `json:"status"`
	ExpiresAt int64     `json:"expiresAt"`
	Customer  *Customer `json:"customer,omitempty"`
}
type ViewCredential struct {
	Token      string   `json:"token"`
	AccountID  string   `json:"accountId"`
	InstanceID string   `json:"instanceId"`
	ContextID  string   `json:"contextId"`
	Scopes     []string `json:"scopes"`
	ExpiresAt  int64    `json:"expiresAt"`
}
type SitePatch struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision string `json:"expectedRevision"`
	Name             string `json:"name"`
	SiteURL          string `json:"siteUrl"`
}
type SiteOperation struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision string `json:"expectedRevision"`
}
type Site struct {
	SiteID            string `json:"siteId"`
	InstanceID        string `json:"instanceId"`
	Name              string `json:"name"`
	SiteURL           string `json:"siteUrl"`
	VerifiedOrigin    string `json:"verifiedOrigin"`
	VerifiedAt        int64  `json:"verifiedAt"`
	VerificationState string `json:"verificationState"`
	ConnectionState   string `json:"connectionState"`
	Revision          string `json:"revision"`
	LastSyncAt        int64  `json:"lastSyncAt"`
	CreatedAt         int64  `json:"createdAt"`
}
type SitePage struct {
	Items      []Site `json:"items"`
	NextCursor string `json:"nextCursor"`
}
type SiteChallenge struct {
	SchemaVersion int    `json:"schemaVersion"`
	ChallengeID   string `json:"challengeId"`
	InstanceID    string `json:"instanceId"`
	Proof         string `json:"proof"`
}
type Verification struct {
	Challenge        SiteChallenge `json:"challenge"`
	SiteID           string        `json:"siteId"`
	ExpectedRevision string        `json:"expectedRevision"`
	Origin           string        `json:"origin"`
	ExpiresAt        int64         `json:"expiresAt"`
}
type Order struct {
	OrderNo    string `json:"orderNo"`
	PluginID   string `json:"pluginId"`
	InstanceID string `json:"instanceId"`
	SiteID     string `json:"siteId"`
	Amount     string `json:"amount"`
	Currency   string `json:"currency"`
	State      string `json:"state"`
	Trial      bool   `json:"trial"`
	ExpiresAt  int64  `json:"expiresAt"`
	CreatedAt  int64  `json:"createdAt"`
	LastError  string `json:"lastError"`
}
type OrderPage struct {
	Items      []Order `json:"items"`
	NextCursor string  `json:"nextCursor"`
}
type Entitlement struct {
	LicenseID           string `json:"licenseId"`
	PluginID            string `json:"pluginId"`
	InstanceID          string `json:"instanceId"`
	SiteID              string `json:"siteId"`
	Revision            string `json:"revision"`
	Source              string `json:"source"`
	Status              string `json:"status"`
	Domain              string `json:"domain"`
	NotBefore           int64  `json:"notBefore"`
	ExpiresAt           int64  `json:"expiresAt"`
	MinVersion          string `json:"minVersion"`
	MaxVersionExclusive string `json:"maxVersionExclusive"`
}
type EntitlementPage struct {
	Items      []Entitlement `json:"items"`
	NextCursor string        `json:"nextCursor"`
}
type Version struct {
	Version          string `json:"version"`
	Mode             string `json:"mode"`
	MinCore          string `json:"minCore"`
	MaxCoreExclusive string `json:"maxCoreExclusive"`
}
type Price struct {
	Revision        string `json:"revision"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	DurationSeconds int64  `json:"durationSeconds"`
	TrialSeconds    int64  `json:"trialSeconds"`
}
type Product struct {
	PluginID    string    `json:"pluginId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Publisher   string    `json:"publisher"`
	Sellable    bool      `json:"sellable"`
	Price       *Price    `json:"price,omitempty"`
	Versions    []Version `json:"versions"`
}
type ProductPage struct {
	Items      []Product `json:"items"`
	NextCursor string    `json:"nextCursor"`
}
type Capabilities struct {
	APIVersion       string `json:"apiVersion"`
	CustomerAccounts bool   `json:"customerAccounts"`
	AccountView      bool   `json:"accountView"`
	PublicProducts   bool   `json:"publicProducts"`
}
type PublicProfile struct {
	ProfileID         string            `json:"profileId"`
	Origin            string            `json:"origin"`
	DistributionRoots map[string]string `json:"distributionRoots"`
	LicenseIssuer     string            `json:"licenseIssuer"`
	LicenseRoots      map[string]string `json:"licenseRoots"`
}
type Failure struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	RequestID         string `json:"requestId"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
}

// Listing is the richer catalog contract. Original Product responses stay unchanged.
type ListingQuery struct {
	Cursor      string `json:"cursor,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Query       string `json:"q,omitempty"`
	Category    string `json:"category,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}
type Release struct {
	Version     string `json:"version"`
	PublishedAt int64  `json:"publishedAt"`
	Status      string `json:"status"`
	Notes       string `json:"notes"`
}
type Listing struct {
	Product
	Category    string    `json:"category"`
	Kind        string    `json:"kind"`
	Recommended bool      `json:"recommended"`
	Images      []string  `json:"images"`
	Details     string    `json:"details"`
	BillingMode string    `json:"billingMode"`
	History     []Release `json:"history"`
}
type ListingPage struct {
	Items      []Listing `json:"items"`
	NextCursor string    `json:"nextCursor"`
}
type Facets struct {
	Categories []string `json:"categories"`
	Kinds      []string `json:"kinds"`
}

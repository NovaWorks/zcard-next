package marketaccount

// Host DTOs contain a browser-context proof, never the remote account-view token.
type HostContext struct {
	ContextID     string `json:"contextId"`
	ContextSecret string `json:"contextSecret"`
}
type HostBegin struct {
	ContextID     string `json:"contextId"`
	ContextSecret string `json:"contextSecret"`
	UserCode      string `json:"userCode"`
	ExpiresAt     int64  `json:"expiresAt"`
	PollSeconds   int    `json:"pollSeconds"`
}
type HostConfirm struct {
	ContextID     string `json:"contextId"`
	ContextSecret string `json:"contextSecret"`
	AccountID     string `json:"accountId"`
}
type HostPage struct {
	ContextID     string `json:"contextId"`
	ContextSecret string `json:"contextSecret"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}
type CustomerPath struct {
	AccountID string `json:"accountId"`
}
type CustomerStatus struct {
	RequestID        string `json:"requestId"`
	ExpectedRevision string `json:"expectedRevision"`
	Suspended        bool   `json:"suspended"`
	Reason           string `json:"reason"`
}
type CustomerRecord struct {
	Customer          Customer `json:"customer"`
	Revision          string   `json:"revision"`
	Origin            string   `json:"origin"`
	ProvisioningState string   `json:"provisioningState"`
	CreatedAt         int64    `json:"createdAt"`
}
type CustomerPage struct {
	Items      []CustomerRecord `json:"items"`
	NextCursor string           `json:"nextCursor"`
}

export default {
  hours: "hours",
  minutes: "minutes",
  seconds: "seconds",

  recommended: "Recommended",
  category: "Category",
  kind: "Type",
  perpetual: "Lifetime purchase",
  days: "days",
  releaseHistory: "Release history",
  withdrawn: "Withdrawn",
  published: "Published",
  updatesTab: "Updates",
  settingsTab: "Plugin settings",
  marketAddressHelp:
    "Connects to the official market by default. Unbind this site before switching markets.",
  marketAddressTrust:
    "Changing the URL does not trust new signing keys. Installation requires trusted distribution and license keys.",
  checkUpdates: "Check for updates",
  noUpdates: "No compatible updates available",
  permanentLicense: "Permanent license",

  workspaceIntro: "Discover plugins for your website",
  loginShort: "Sign in",
  registerShort: "Register",
  logoutShort: "Sign out",
  walletBalance: "Balance",
  walletUnavailable: "Unavailable",
  marketSignIn: "Sign in to the marketplace",
  authWelcome: "Sign in to manage your plugins and licenses",
  priceFilter: "Price filter",
  allPlugins: "All",
  freePlugins: "Free",
  paidPlugins: "Paid",
  loadedPlugins: "Loaded",
  showingPlugins: "Showing",
  productDescriptionFallback:
    "View details, compatible versions and required permissions.",
  loadedFilterHelp: "Filtering loaded plugins. Load more to continue browsing.",

  sourceLegacy: "Legacy grant",
  runtimeDetails: "Runtime details and permissions",
  advanced: "Advanced settings and offline maintenance",
  installedTab: "Installed",
  accountTab: "Market account",
  accountIntro:
    "Sign in for temporary account access. Site binding and plugin licenses require separate confirmation.",
  accountUnavailable:
    "Account query unavailable. Retry or sign in again; this does not mean there are no purchases.",
  accountStart: "Sign in / register",
  accountPoll: "Check authorization",
  accountRefresh: "Refresh account records",
  accountLogout: "Sign out of account view",
  accountCode: "Account access code",
  accountOpen: "Open official sign in / registration",
  copyCode: "Copy code",
  copyLink: "Copy link",
  copied: "Copied",
  popupHelp:
    "Sign in and approve access in the official window, then confirm your account here. If the popup is blocked, use the link above; the code is included automatically.",
  accountConfirmHelp: "I have verified this market account",
  accountConfirm: "Confirm and view account",
  accountRestart: "Request incomplete or expired. Start sign in again.",
  emailVerified: "Email verified",
  emailUnverified: "Email not verified",
  customerCenter: "Official customer center: purchases, renewals and sites",
  currentSiteConnection: "Current site connection and license sync",
  siteChallenge: "Public domain verification",
  challengeHelp:
    "Start verification in the official site manager. Paste the challenge JSON here, publish it, then check on the official page. Private sites may remain unverified.",
  challengePublish: "Publish challenge",
  challengeReady:
    "Published. Return to the official customer center to check verification.",
  challengeFailed: "Challenge invalid, expired or for another instance.",
  accountListFailed:
    "Could not load records. Private data is hidden; refresh to retry.",
  purchases: "Purchase history",
  accountEntitlements: "Account plugin entitlements",
  accountSites: "Authorized sites",
  noRecords: "No records",
  orderFulfilled: "Purchased",
  orderRefunded: "Refunded",
  orderRefunding: "Refund processing",
  siteDisconnected: "Disconnected",
  siteVerified: "Domain verified",
  siteUnverified: "Domain unverified",
  sourcePurchase: "Purchased",
  sourceTrial: "Trial",
  sourceManual: "Manual grant",
  sourceTest: "Test grant",
  purchasedHelp:
    "Purchase, valid license, installed and enabled are separate states. After purchasing, sync this site’s licenses and preview installation. Retry failures without buying again.",
  accountMismatch:
    "The signed-in account differs from the site owner. Sign in with the original account; switching views does not transfer the site or licenses.",
  accountSetup: "Sign in and connect this site",
  purchase: "Buy / try on official market",
  marketSettings: "Custom market origin",
  marketSearch: "Search plugins",
  notInstalled: "Not installed",
  productDetail: "Details and versions",
  selectVersion: "Choose version and check core compatibility",

  marketPurchaseHelp:
    "Already purchased? Refresh owned entitlements, then preview and install. If the market is offline or a download fails, retry later without purchasing again.",
  marketPaid: "Plugin license required",
  bindingSafetyCheck: "Last safety policy check",
  bindingTitle: "Marketplace account binding",
  bindingIntro:
    "Both the market account and instance administrator must confirm. Credentials stay server-side; unbinding does not change signed offline license expiry.",
  bindingBound: "Bound",
  bindingApproved: "Market approved; confirm the account on this instance",
  bindingPending: "Waiting for the market account to approve the code",
  bindingConfirming:
    "Confirmation pending recovery; verify the account and retry",
  bindingRotating: "Credential rotation pending recovery; retry",
  bindingRevoking: "Remote unbinding unconfirmed; retry",
  bindingPairExpired: "Pairing expired; cancel and start again",
  bindingCredentialExpired: "Online credential expired; unbind and pair again",
  bindingRecoveryRequired:
    "Credential cannot be decrypted; restore the data key or recover the account",
  bindingStarting: "Pairing request pending recovery; retry start",
  bindingUnbound: "Unbound",
  bindingLoadFailed: "Could not read binding status. Refresh to retry.",
  bindingActionFailed:
    "Operation not confirmed. Check the market address, account and network, then retry. Wait at least 5 seconds between polls. Use account recovery for invalid credentials.",
  bindingSaved: "Operation confirmed and state updated.",
  bindingMarket: "Market",
  bindingCode: "Temporary pairing code",
  bindingOpenMarket: "Open market account confirmation",
  bindingAccount: "Market account",
  bindingExpires: "Pairing or online credential expires",
  bindingLastSync: "Last entitlement sync",
  bindingNever: "Not synced yet",
  bindingConfirm:
    "I have verified this market account and confirm the selected binding or unbinding operation",
  bindingStart: "Start / retry pairing",
  bindingPoll: "Check market approval",
  bindingFinish: "Confirm account and finish binding",
  bindingCancel: "Cancel pairing",
  bindingSync: "Refresh owned entitlements",
  bindingRotate: "Rotate / recover credential",
  bindingRevoke: "Unbind / retry unbinding",
  bindingRecovery: "Lost credential and account recovery",
  bindingRecoveryHelp:
    "First open the market page and sign in to the original account. Revoke the remote binding using this instance ID, then clear local state and pair again. Clearing local state alone does not revoke remote credentials or offline licenses.",
  bindingRecoveryConfirm:
    "I understand clearing local state does not revoke remote credentials and have handled market account recovery",
  bindingForget: "Clear local binding state",

  licenseTitle: "Plugin entitlements",
  licenseIntro:
    "Paid plugins have independent licenses. Updating a license resumes enabled plugins with no other blocking faults, without reinstalling.",
  licenseInstance: "Instance ID",
  licenseLoadFailed: "Could not load entitlement status. Retry.",
  licenseFileLimit: "Choose a license JSON file up to 64 KiB.",
  licenseInstallFailed:
    "License verification failed. Check trusted keys, instance, domain and revision. Existing licenses are unchanged.",
  licenseInstalled:
    "License document saved. Check plugin status; expired or revoked documents do not resume execution.",
  licenseNotReady:
    "Licensing has not initialized. Check instance configuration.",
  licenseEmpty:
    "No paid plugin licenses. Installed free versions are unaffected.",
  licenseFile: "Offline license / signed safety policy JSON",
  licenseInstall: "Verify and update license",
  licenseValid: "Valid license",
  licenseMissing: "Valid plugin license required",
  licenseNotYetValid: "License not yet valid",
  licenseIncompatible: "License does not cover this plugin version",
  securityRevoked: "Plugin blocked by safety policy",
  licenseExpires: "Expires",
  licenseIssuer: "Issuer",
  licenseRevision: "Revision",
  licenseRenew: "Buy / renew",
  licensePurchasePending:
    "Purchases open in P7. Only controlled test and gift licenses are available now.",
  confirmPaid:
    "I confirm installing this paid version. Activation requires a valid license; existing free versions are not automatically converted.",

  marketSave: "Save market origin",
  marketTitle: "App marketplace",
  marketIntro:
    "Install from a trusted market. Bind an account to view licensed versions. Valid signed licenses remain usable while the market is offline.",
  marketOrigin: "Market origin",
  marketRefresh: "Refresh catalog",
  marketAccessRequired:
    "This version is not available for download. For paid plugins, purchase from the market, bind this site and sync its entitlement first.",
  marketLastChecked: "Catalog checked at",
  marketMore: "Load more",
  marketPriceHint: "Display price; checkout uses the official quote",
  marketUnavailable: "Currently unavailable for purchase",
  marketUnconfigured:
    "Official market is not configured. Provision a public profile or configure a custom market here. Installed plugins remain manageable.",
  marketOffline:
    "The market is unavailable or catalog verification failed. Installed plugins continue under their existing license and safety policies.",
  marketConfigError:
    "Unable to read or save market settings. Check the HTTPS origin and permissions; unbind or cancel pairing before changing the origin.",
  marketInspectError:
    "Sync a valid plugin entitlement first. Then check package signatures, withdrawn versions and core compatibility if inspection still fails.",
  marketRevision: "Catalog revision",
  marketEmpty: "No published plugins",
  marketFree: "Free",
  marketPreview: "Inspect installation package",
  marketEnableHint:
    "Enable a new installation in the plugin list. Upgrades preserve its enabled state.",
  marketApprove:
    "I checked the source, version and permissions and approve this installation or upgrade.",

  title: "Plugins",
  intro:
    "Install trusted signed plugins and manage product extensions. Disabling a plugin keeps purchase restrictions.",
  refresh: "Refresh status",
  loading: "Loading…",
  loadFailed: "Loading failed. Content may be stale; please retry.",
  empty: "No plugins installed",
  readonly:
    "Installation and lifecycle changes require a main-site instance administrator with plugin management permission.",
  upload: "Upload signed package",
  descriptor: "Descriptor file (descriptor.json)",
  signature: "Signature file (signature.ed25519)",
  archive: "Plugin archive (plugin.zplug)",
  verify: "Verify and preview",
  verified: "Signature and compatibility verified",
  badFiles:
    "Select all three files: descriptor up to 64 KiB, signature exactly 64 bytes, archive up to 8 MiB.",
  verifyFailed:
    "Signature or compatibility check failed. Check the files, host version and trusted keys.",
  approve: "I approve the plugin permissions listed below",
  scopes: "Plugin permissions",
  newScopes: "New permissions",
  import: "Install without activating",
  enable: "Activate",
  upgrade: "Upgrade",
  rollback: "Roll back",
  disable: "Disable",
  uninstall: "Uninstall (keep rules)",
  active: "Active",
  disabled: "Disabled / staged",
  uninstalled: "Uninstalled; rules retained",
  failed: "Unavailable",
  pending: "Awaiting confirmation",
  desired: "Desired package",
  observed: "Confirmed package (see runtime state above)",
  generation: "Generation",
  phase: "Operation phase",
  impact: "Products with retained restrictions",
  affected: "Affected products",
  currentSite:
    "Only products in the current site are listed below; the total may include other sites.",
  truncated:
    "Showing the first 1,000 products. Use product management for the rest.",
  impactFailed:
    "Unable to load impact. The operation is blocked; refresh and retry.",
  confirmAction: "Confirm plugin operation",
  impactWarning:
    "After disabling or uninstalling, existing restrictions remain and affected products cannot accept new orders. Release restrictions separately.",
  switchWarning:
    "This changes purchase validation for the affected products. Preparation failures keep the previous runtime.",
  confirm: "Confirm",
  cancel: "Cancel",
  operation: "Operation ID",
  query: "Check original operation",
  unknown:
    "The result is unconfirmed. Query the original operation ID before starting another operation.",
  operationFailed:
    "Operation failed. Refresh to inspect the actual current state.",
  operationDone: "Operation confirmed. Current state is shown below.",
  notFoundOperation:
    "Original operation not found yet. Query again later, or verify current state before clearing this record.",
  clearOperation: "State checked; clear record",
  clearWarning:
    "Clearing this record does not cancel the server operation. Have you verified the current runtime state?",
  product: "Product",
  openProduct: "View product restrictions",
  extensions: "Plugin extensions",
  saveFirst: "Save the product to obtain its ID before configuring extensions.",
  noRead:
    "Viewing extensions requires plugin read and product edit permissions.",
  noConfigure: "Your account cannot configure plugins. Settings are read-only.",
  locked:
    "The product is locked. Unlock it in product management with the appropriate permission.",
  restricted: "Host purchase restriction is in force",
  unrestricted: "No host purchase restriction for this plugin",
  inactive:
    "The plugin is inactive or unavailable. Existing restrictions continue to block purchases.",
  noContributions:
    "No product extensions are available. Host rule recovery remains accessible below.",
  incompatible:
    "Unsupported configuration schema or control. Saving is blocked; existing purchase restrictions remain.",
  invalidConfig:
    "Stored configuration is unreadable. Overwriting is blocked; use the separate release action to restore purchases.",
  exactLevels:
    "Only selected effective levels qualify. Higher levels are not included automatically; upgrading to an unselected level also denies access.",
  chooseLevels: "Select allowed levels",
  disabledLevel: "Disabled or unavailable",
  save: "Save extension settings",
  saved: "Extension settings saved and applied separately",
  invalidSelection:
    "Select at least one enabled level when restricting purchases (up to 100).",
  saveFailed:
    "Save unconfirmed; draft retained. Refresh to check permissions, product locks and server configuration before retrying.",
  changed:
    "Server version or configuration changed. Your draft is retained; compare before continuing.",
  draft: "Local draft",
  remote: "Latest server settings",
  useRemote: "Use server settings",
  keepDraft: "Continue draft on latest version",
  release: "Release purchase restriction",
  releaseConfirm: "Confirm product restriction release",
  releaseWarning:
    "Release only this plugin restriction on this product in the current site. This does not enable the plugin or release other rules.",
  released: "Purchase restriction released",
  noRelease:
    "Releasing a restriction requires plugin:release and product edit permissions.",
  discardTitle: "Discard unsaved extension draft?",
  discard:
    "Separately saved settings remain in effect. Unsaved extension inputs will be discarded.",
  saveDraftFirst:
    "Plugin extensions have an unsaved draft. Save or discard it first.",
  packageMissing: "Signed package missing or damaged",
  runtimeFault: "Plugin runtime fault",
  expired: "Entitlement expired",
  revoked: "Entitlement revoked",
  packageIncompatible: "Plugin is incompatible with this host",
  permissionChanged:
    "Permissions or site scope changed. Refresh before continuing.",
  details: "Details",
  version: "Plugin version",
};

// P2 runtime fixture: evaluated only in the isolated trusted-plugin test VM.
function evaluate(input) {
  if (!input.config.enabled) return { allow: true, reason: "OK" };
  if (input.channel !== "storefront") return { allow: false, reason: "SUPPLY_RESTRICTED" };
  if (!input.member.authenticated) return { allow: false, reason: "LOGIN_REQUIRED" };
  if (input.config.allowedLevelIds.indexOf(input.member.effectiveLevelId) < 0)
    return { allow: false, reason: "MEMBER_LEVEL_DENIED" };
  return { allow: true, reason: "OK" };
}

// Test-owned boundary matching the pinned instance-settings and health API.
// Special fixture bearer tokens select deployment policies, never new API fields.
const environmentId = "11111111-1111-4111-8111-111111111111";
const flags = {
  enableEnvironments: false, enableNativeRunner: true, enableManagedSandboxOnly: false,
  enableIsolatedWorkspaces: false, enableIsolatedWorkspacesByDefault: false,
  enableStreamlinedLeftNavigation: true, enableStreamlinedUi: true, enableApps: true,
  enableChatConnectors: false, enableMcpAggregators: false, enablePipelines: false,
  enableCases: false, enableAgentChat: false, enableConferenceRoomChat: false,
  enableClassicTaskInterface: false, enableIssuePlanDecompositions: false,
  enableExperimentalFileViewer: false, enableExternalObjects: false, enableSmokeLab: false,
  enableBuiltInAgents: false, enableBetaSkills: false, enableSummaries: false,
  enableStatusCards: false, enableDecisions: false, enableGoalsSidebarLink: false,
  enableServerInfoDebugView: false, enablePaperclipDeveloperMode: false,
  enableSimplifiedEnglishInteractions: false, enableFirstTaskPlanProposal: false,
  autoRestartDevServerWhenIdle: false, enableWorkspaceBranchReconcileForward: true,
  enableWorkspaceDirtyQuarantineRepair: true, enableOwnerInstanceAdmin: false,
  enableSandboxDuplexBridge: false, enableRunnerPreviewIngress: false,
  enableWorktreeRunExecution: false, worktreeRunExecutionActivatedAt: null,
  worktreeRunExecutionActivationInstanceId: null,
};
const settings = {
  id: "33333333-3333-4333-8333-333333333333", defaultEnvironmentId: null,
  general: { censorUsernameInLogs: false, keyboardShortcuts: false,
    feedbackDataSharingPreference: "prompt", backupRetention: {dailyDays:7,weeklyWeeks:4,monthlyMonths:1} },
  experimental: {...flags}, createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z",
};
const attempts = [];
module.exports = function instanceSettings(req, res, body) {
  const token = req.headers.authorization ?? "";
  if (!token.startsWith("Bearer settings-")) return false;
  const p = new URL(req.url, "http://fixture").pathname;
  const send = (value, status=200) => {res.writeHead(status);res.end(JSON.stringify(value));return true;};
  const fail = (error,code,status=403) => send({error,details:{code}},status);
  const hiddenSettings = token.endsWith("hidden") ? ["instance.general.keyboardShortcuts"]
    : token.endsWith("hidden-page") ? ["instance.experimental"] : [];
  const cloud = token.endsWith("cloud") ? {managed:true,managedBy:"paperclip-cloud",stackSlug:"fixture",cloudBaseUrl:"https://example.test"} : undefined;
  const managedKeys = token.endsWith("managed") ? {enableEnvironments:{managed:true,managedBy:"paperclip-cloud"}} : undefined;
  const experimental = () => ({...settings.experimental,...(managedKeys ? {enableEnvironments:false,managedKeys}: {})});
  if (p === "/api/health" && req.method === "GET") return send({status:"ok",...(hiddenSettings.length ? {hiddenSettings}:{}),...(cloud ? {cloud}: {})});
  if (p === "/api/test/instance-settings" && req.method === "GET") return send({settings,attempts,flags,environmentId});
  if (p.startsWith("/api/environments/") && req.method === "GET") {
    const id = p.split("/").pop();
    if (![environmentId,"22222222-2222-4222-8222-222222222222"].includes(id)) return send({error:"Environment not found"},404);
    return send({id,name:"Fixture",driver:"ssh",status:id===environmentId ? "active":"archived",config:{},envVars:{},metadata:null,description:null});
  }
  if (!["/api/instance/settings","/api/instance/settings/general","/api/instance/settings/experimental"].includes(p)) return false;
  const section=p.split("/")[4];
  if (req.method === "GET") return send(section === "general" ? settings.general : section === "experimental" ? experimental() : {...settings,experimental:experimental()});
  if (req.method !== "PATCH") return send({error:"Route not found"},404);
  attempts.push({path:p,body});
  if (token.endsWith("non-admin")) return fail("Instance admin access required","forbidden");
  const current=section === "general" ? settings.general : section === "experimental" ? experimental() : settings;
  for (const key of Object.keys(body)) {
    if ((hiddenSettings.includes("instance."+section+"."+key) || section==="experimental" && hiddenSettings.includes("instance.experimental")) && JSON.stringify(body[key])!==JSON.stringify(current[key])) return fail(key+" is managed by the hosting operator on this instance","settings_operator_managed");
  }
  if (section === "general") {
    if (cloud && body.executionMode !== undefined && body.executionMode !== (settings.general.executionMode ?? "any")) return fail("executionMode is platform-managed on cloud-managed instances","execution_mode_platform_managed");
    if (Object.keys(body).some(key=>!["censorUsernameInLogs","keyboardShortcuts","feedbackDataSharingPreference","backupRetention","executionMode"].includes(key))) return fail("Unrecognized key","validation_error",400);
    Object.assign(settings.general,body);
    return send(settings.general);
  }
  if (section === "experimental") {
    // The real PATCH strips unknown keys; managed writes persist underneath overlays.
    for (const [key,value] of Object.entries(body)) if (typeof flags[key] === "boolean") settings.experimental[key]=value;
    return send(experimental());
  }
  if (Object.keys(body).some(key=>key!=="defaultEnvironmentId")) return fail("Unrecognized key","validation_error",400);
  if (body.defaultEnvironmentId !== null && body.defaultEnvironmentId !== environmentId) return fail("Environment is archived or missing","invalid_environment",422);
  Object.assign(settings,body);
  return send({...settings,experimental:experimental()});
};

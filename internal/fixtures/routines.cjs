// Test-owned boundary fixture for the pinned routine routes. It intentionally
// offers only native setupPending acknowledgement, never staged rotation or rollback.
const routines = {};
const triggers = {};
const events = [];
let sequence = 1;
const id = () => `00000000-0000-4000-8000-${String(sequence++).padStart(12, "0")}`;
const clone = value => JSON.parse(JSON.stringify(value));
const templateNames = (title, description) => [...new Set([title, description].filter(Boolean).flatMap(s => [...s.matchAll(/\{\{\s*([A-Za-z](?:\\_|[A-Za-z0-9_])*)\s*\}\}/g)].map(m => m[1].replaceAll("\\_", "_"))))].filter(n => n !== "date" && n !== "timestamp");
module.exports = (req, res, body) => {
  const path = new URL(req.url, "http://127.0.0.1").pathname;
  const reply = (status, value) => { res.writeHead(status); res.end(value === undefined ? undefined : JSON.stringify(value)); return true; };
  const detail = r => ({ ...r, triggers: Object.values(triggers).filter(t => t.routineId === r.id && !t.archived) });
  if (path === "/api/test/routines" && req.method === "GET") return reply(200, { routines: Object.values(routines), triggers: Object.values(triggers), events });
  if (path === "/api/test/routines/delivery" && req.method === "POST") {
    if (body.value !== "routine-webhook-secret-must-never-appear") return reply(400, { error: "one-time webhook secret was not delivered" });
    if (!Object.values(triggers).some(t => t.id === body.name && t.setupPending)) return reply(409, { error: "delivery must be bound to the pending producing trigger" });
    events.push({ method: "DELIVERY", path, triggerId: body.name });
    return reply(200, { delivered: true });
  }
  if (path === "/api/test/routines/conflict" && req.method === "POST") {
    routines[body.id].title = "Concurrent edit";
    routines[body.id].variables = [];
    routines[body.id].latestRevisionId = id();
    return reply(200, { changed: true });
  }
  if (path === "/api/test/routines/webhook" && req.method === "POST") {
    const t = { id: "00000000-0000-4000-8000-000000009999", routineId: body.routineId, companyId: "comp-1", kind: "webhook", label: null, enabled: false, setupPending: false, cronExpression: null, timezone: null, signingMode: "bearer", replayWindowSec: 300 };
    triggers[t.id] = t;
    routines[t.routineId].latestRevisionId = id();
    return reply(200, t);
  }
  if (path === "/api/companies/comp-1/routines" && req.method === "POST") {
    if (Object.keys(body).some(k => !["title", "description", "projectId", "folderId", "goalId", "parentIssueId", "assigneeAgentId", "priority", "status", "concurrencyPolicy", "catchUpPolicy", "activityGatePolicy", "activityGateScope", "variables", "env"].includes(k))) return reply(400, { error: "Unknown native routine field" });
    const variables = templateNames(body.title, body.description).map(name => ({ name, label: null, type: "text", defaultValue: null, required: true, options: [], ...body.variables.find(v => v.name === name) }));
    const r = { id: id(), companyId: "comp-1", ...body, variables, latestRevisionId: id() };
    if (r.status === "active" && !r.assigneeAgentId) r.status = "paused";
    routines[r.id] = r;
    events.push({ method: req.method, path, body: clone(body) });
    return reply(201, r);
  }
  const routineMatch = path.match(/^\/api\/routines\/([^/]+)(\/triggers)?$/);
  if (routineMatch) {
    const r = routines[routineMatch[1]];
    if (!r) return reply(404, { error: "Routine not found" });
    if (!routineMatch[2] && req.method === "GET") return reply(200, detail(r));
    if (!routineMatch[2] && req.method === "PATCH") {
      if (Object.keys(body).some(k => !["baseRevisionId", "title", "description", "projectId", "folderId", "goalId", "parentIssueId", "assigneeAgentId", "priority", "status", "concurrencyPolicy", "catchUpPolicy", "activityGatePolicy", "activityGateScope", "variables", "env"].includes(k))) return reply(400, { error: "Unknown native routine field" });
      if (body.baseRevisionId && body.baseRevisionId !== r.latestRevisionId) return reply(409, { error: "Routine was updated by someone else", details: { currentRevisionId: r.latestRevisionId } });
      events.push({ method: req.method, path, body: clone(body) });
      const { baseRevisionId, ...patch } = body;
      Object.assign(r, patch, { latestRevisionId: id() });
      return reply(200, r);
    }
    if (routineMatch[2] && req.method === "POST") {
      const kindFields = { schedule: ["cronExpression", "timezone"], api: [], webhook: ["setupPending", "signingMode", "replayWindowSec"] };
      if (!kindFields[body.kind] || Object.keys(body).some(k => !["kind", "label", "enabled", ...kindFields[body.kind]].includes(k))) return reply(400, { error: "Invalid native trigger fields" });
      events.push({ method: req.method, path, body: clone(body) });
      const t = { id: id(), routineId: r.id, companyId: r.companyId, label: null, enabled: true, cronExpression: null, timezone: null, signingMode: null, replayWindowSec: null, ...body };
      triggers[t.id] = t;
      r.latestRevisionId = id();
      return reply(201, { trigger: t, secretMaterial: body.kind === "webhook" ? { webhookUrl: "https://example.test/hooks/fixture", webhookSecret: "routine-webhook-secret-must-never-appear" } : null });
    }
  }
  const triggerMatch = path.match(/^\/api\/routine-triggers\/([^/]+)(\/rotate-secret)?$/);
  if (triggerMatch) {
    const t = triggers[triggerMatch[1]];
    if (!t) return reply(404, { error: "Routine trigger not found" });
    if (!triggerMatch[2] && req.method === "PATCH") {
      if (Object.keys(body).some(k => !["label", "enabled", "setupPending", "archived", "cronExpression", "timezone", "signingMode", "replayWindowSec"].includes(k)) || ("setupPending" in body && body.setupPending !== false)) return reply(400, { error: "Invalid native trigger patch" });
      if (t.kind === "schedule" && (("cronExpression" in body && !body.cronExpression) || ("timezone" in body && !body.timezone))) return reply(422, { error: "Scheduled triggers require cronExpression and timezone" });
      events.push({ method: req.method, path, body: clone(body) });
      Object.assign(t, body);
      routines[t.routineId].latestRevisionId = id();
      return reply(200, t);
    }
    if (!triggerMatch[2] && req.method === "DELETE") {
      events.push({ method: req.method, path });
      delete triggers[t.id];
      routines[t.routineId].latestRevisionId = id();
      return reply(204);
    }
    if (triggerMatch[2] && req.method === "POST") {
      if (t.kind !== "webhook") return reply(400, { error: "Only webhook triggers have secrets" });
      events.push({ method: req.method, path, body: clone(body) });
      routines[t.routineId].latestRevisionId = id();
      return reply(200, { trigger: t, secretMaterial: { webhookUrl: "https://example.test/hooks/fixture", webhookSecret: "routine-webhook-secret-must-never-appear" } });
    }
  }
  return false;
};

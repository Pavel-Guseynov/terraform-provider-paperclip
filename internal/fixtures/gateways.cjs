// Test-owned boundary matching the pinned named-gateway routes. No exact GET,
// DELETE, token PATCH, archived inventory, or automatic binding removal exists.
const gateways = [];
const tokens = [];
const events = [];
let sequence = 0;
const profileID = "42400000-0000-4000-8000-000000000001";
module.exports = function gatewayAPI(req, res, body, state) {
  const p = new URL(req.url, "http://fixture").pathname;
  const send = (data, status = 200) => { res.writeHead(status); res.end(JSON.stringify(data)); return true; };
  const fail = (error, status = 422) => {
    console.error(JSON.stringify({timestamp:new Date().toISOString(),time_unix_nano:String(BigInt(Date.now())*1000000n),level:"error",service:"provider-gateway-fixture",action:"request",message:error,attributes:{method:req.method,path:p,status,value_present:typeof body.value === "string" && body.value.length > 0},error:{type:"FixtureBoundaryError",message:error,traceback:"not applicable: explicit fixture rejection"}}));
    return send({error}, status);
  };
  const record = (action, id) => events.push({action, id});
  const view = g => ({...g, tokens: tokens.filter(t => t.gatewayId === g.id)});
  if (p === "/api/test/gateway-seed" && req.method === "POST") {
    state.profiles.push({id:profileID, companyId:"comp-1", profileKey:"gateway-fixture", name:"Gateway profile", status:"active", defaultAction:"deny", metadata:{}, entries:[], bindings:[]});
    return send({});
  }
  if (p === "/api/test/gateways" && req.method === "GET") return send({gateways, tokens, events});
  if (p === "/api/test/gateway-delivery" && req.method === "POST") {
    const token = tokens.find(t => body.value === `pcgw_${t.id}.fixturesecret`);
    if (!token || token.revokedAt) return fail(body.value ? "write-only delivery received an unrecognized or revoked token" : "write-only delivery received no value from the consumer configuration");
    record("deliver", token.id); return send({});
  }
  if (p === "/api/companies/comp-1/tools/gateways") {
    if (req.method === "GET") return send({gateways:gateways.filter(g => g.status !== "archived").map(view)});
    if (req.method === "POST") {
      if (!state.profiles.some(p => p.id === body.profileId)) return fail("Gateway profile must belong to the company");
      if (gateways.some(g => g.displaySlug === body.displaySlug)) return fail("Duplicate gateway slug",409);
      const g = {...body, id:`42400000-0000-4000-8000-${String(++sequence).padStart(12,"0")}`, companyId:"comp-1", status:"active", archivedAt:null};
      gateways.push(g);
      state.profiles.find(p => p.id === body.profileId).bindings.push({id:`binding-${g.id}`,companyId:"comp-1",profileId:body.profileId,targetType:"gateway",targetId:g.id,priority:10,metadata:{source:"named_mcp_gateway"}});
      record("create",g.id); return send(view(g),201);
    }
  }
  const gatewayMatch = p.match(/^\/api\/tool-gateway\/gateways\/([^/]+)$/);
  if (gatewayMatch && req.method === "PATCH") {
    if (body.companyId !== "comp-1") return fail("companyId is required",400);
    const g = gateways.find(g => g.id === gatewayMatch[1]); if (!g) return fail("MCP gateway not found",404);
    if (body.profileId && !state.profiles.some(p => p.id === body.profileId)) return fail("Gateway profile must belong to the company");
    if (body.profileId && g.profileId !== body.profileId) state.profiles.find(p => p.id === body.profileId).bindings.push({id:`binding-${sequence}`,companyId:"comp-1",profileId:body.profileId,targetType:"gateway",targetId:g.id,priority:10,metadata:{source:"named_mcp_gateway"}});
    Object.assign(g,body); record(body.status === "archived" ? "archive" : "update",g.id); return send(view(g));
  }
  const mintMatch = p.match(/^\/api\/tool-gateway\/gateways\/([^/]+)\/tokens$/);
  if (mintMatch && req.method === "POST") {
    if (body.companyId !== "comp-1") return fail("companyId is required",400);
    if (!gateways.some(g => g.id === mintMatch[1])) return fail("MCP gateway not found",404);
    if (body.subjectType !== "gateway_client" || !body.expiresAt || !Number.isFinite(Date.parse(body.expiresAt))) return fail("Invalid gateway token payload");
    const token = {...body,id:`42400000-0000-4000-8001-${String(++sequence).padStart(12,"0")}`,gatewayId:mintMatch[1],revokedAt:null};
    tokens.push(token); record("mint",token.id); return send({...token,expiresAt:new Date(token.expiresAt).toISOString(),token:`pcgw_${token.id}.fixturesecret`},201);
  }
  const revokeMatch = p.match(/^\/api\/tool-gateway\/gateway-tokens\/([^/]+)\/revoke$/);
  if (revokeMatch && req.method === "POST") {
    if (body.companyId !== "comp-1") return fail("companyId is required",400);
    const token = tokens.find(t => t.id === revokeMatch[1]); if (!token) return fail("MCP gateway token not found",404);
    token.revokedAt = "2026-10-16T00:00:00Z"; record("revoke",token.id); return send(token);
  }
  return false;
};

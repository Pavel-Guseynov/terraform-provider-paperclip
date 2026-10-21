// Test-owned native API boundary for same-apply template planning. State is
// isolated from the other provider-opentofu-test scenarios.
const events = [];
const templates = new Map();
let template;
let connection;
let catalog = [];
module.exports = function stdioPlan(req, res, body) {
  const p = new URL(req.url, "http://fixture").pathname;
  const send = (value, status = 200) => { res.writeHead(status); res.end(JSON.stringify(value)); return true; };
  if (p === "/api/test/stdio-plan") return send({ events, template, connection, catalog });
  if (p === "/api/companies/stdio-plan/tools/applications" && req.method === "GET") {
    return send({ applications: [{ id: "stdio-app", companyId: "stdio-plan", type: "mcp_stdio", status: "active" }] });
  }
  if (p === "/api/companies/stdio-plan/tools/stdio-templates") {
    if (req.method === "GET") return send({ templates: [...templates.values()].filter(item => item.status === "active") });
    if (req.method === "POST") {
      if (templates.has(body.templateId)) return send({ error: "Template identities cannot be reused" }, 409);
      events.push("create template");
      template = { ...body, tools: body.tools ?? [], id: `stdio-template-${templates.size + 1}`, companyId: "stdio-plan", status: "active" };
      templates.set(template.templateId, template);
      return send(template, 201);
    }
  }
  if (p === "/api/companies/stdio-plan/tools/connections" && req.method === "POST") {
    if (!template || template.status !== "active" || body.config.templateId !== template.templateId) {
      return send({ error: "Approved template must exist before connection creation" }, 422);
    }
    events.push("create connection");
    connection = {
      ...body, id: "stdio-connection", companyId: "stdio-plan",
      connectionPurpose: "tool", connectionKind: "managed", ownership: "customer",
      credentialSource: "paperclip_vault", credentialPolicy: "shared", status: body.status ?? "draft", enabled: body.enabled ?? false
    };
    return send(connection, 201);
  }
  if (p === "/api/tool-connections/stdio-connection/catalog/refresh" && req.method === "POST") {
    catalog = template.tools.map(tool => ({
      id: `stdio-entry-${tool.name}`, connectionId: connection.id, toolName: tool.name,
      inputSchema: tool.inputSchema, annotations: tool.annotations
    }));
    return send({ discoveredCount: catalog.length, quarantinedCount: 0, catalog });
  }
  if (p === "/api/tool-connections/stdio-connection/catalog" && req.method === "GET") return send({ catalog });
  if (p === "/api/tool-connections/stdio-connection") {
    if (req.method === "GET") return send(connection);
    if (req.method === "PATCH") {
      if (body.config?.templateId !== template.templateId || template.status !== "active") return send({ error: "Approved template required" }, 422);
      events.push("update connection");
      connection = { ...connection, ...body };
      return send(connection);
    }
    if (req.method === "DELETE") { connection.status = "archived"; return send(connection); }
  }
  if (p.startsWith("/api/companies/stdio-plan/tools/stdio-templates/") && p.endsWith("/disable") && req.method === "POST") {
    const retired = templates.get(p.split("/").at(-2));
    if (!retired) return send({ error: "Template not found" }, 404);
    events.push("disable template");
    retired.status = "disabled";
    return send(retired);
  }
  return false;
};

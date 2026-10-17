// Test-owned native API boundary for same-apply template planning. State is
// isolated from the other provider-opentofu-test scenarios.
const events = [];
let template;
let connection;
module.exports = function stdioPlan(req, res, body) {
  const p = new URL(req.url, "http://fixture").pathname;
  const send = (value, status = 200) => { res.writeHead(status); res.end(JSON.stringify(value)); return true; };
  if (p === "/api/test/stdio-plan") return send({ events, template, connection });
  if (p === "/api/companies/stdio-plan/tools/applications" && req.method === "GET") {
    return send({ applications: [{ id: "stdio-app", companyId: "stdio-plan", type: "mcp_stdio", status: "active" }] });
  }
  if (p === "/api/companies/stdio-plan/tools/stdio-templates") {
    if (req.method === "GET") return send({ templates: template && template.status === "active" ? [template] : [] });
    if (req.method === "POST") {
      events.push("create template");
      template = { ...body, id: "stdio-template", companyId: "stdio-plan", status: "active" };
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
      credentialSource: "paperclip_vault", credentialPolicy: "shared", status: "draft", enabled: false
    };
    return send(connection, 201);
  }
  if (p === "/api/tool-connections/stdio-connection") {
    if (req.method === "GET") return send(connection);
    if (req.method === "DELETE") { connection.status = "archived"; return send(connection); }
  }
  if (p === "/api/companies/stdio-plan/tools/stdio-templates/same-apply/disable" && req.method === "POST") {
    template.status = "disabled";
    return send(template);
  }
  return false;
};

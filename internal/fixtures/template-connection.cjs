// Test-owned native API boundary for connections that carry a gallery template
// source: one created through the custom create path with a declared
// config.sourceTemplateKey and one connected through the app gallery. Paperclip
// refuses in-place updates of these connections, so any update is an error.
// State is isolated from the other provider-opentofu-test scenarios.
const events = [];
const connections = new Map();
const company = "template-connection";
module.exports = function templateConnection(req, res, body) {
  const p = new URL(req.url, "http://fixture").pathname;
  const send = (value, status = 200) => { res.writeHead(status); res.end(JSON.stringify(value)); return true; };
  if (p === "/api/test/template-connection") return send({ events, connections: Object.fromEntries(connections) });
  if (p === `/api/companies/${company}/tools/applications` && req.method === "GET") {
    return send({ applications: [{ id: "github-app", companyId: company, type: "mcp_http", status: "active" }] });
  }
  const base = {
    companyId: company, connectionPurpose: "tool", connectionKind: "managed", ownership: "customer",
    credentialSource: "paperclip_vault", credentialPolicy: "shared", credentialRefs: [], credentialSecretRefs: [], transportConfig: {}
  };
  if (p === `/api/companies/${company}/tools/connections` && req.method === "POST") {
    events.push("create custom");
    const connection = { ...base, ...body, id: "template-custom", status: body.status ?? "draft", enabled: body.enabled ?? false };
    connections.set(connection.id, connection);
    return send(connection, 201);
  }
  if (p === `/api/companies/${company}/tools/apps/connect` && req.method === "POST") {
    events.push(`connect gallery ${body.galleryKey}`);
    const connection = {
      ...base, id: "template-gallery", applicationId: "gallery-app", name: "Hugging Face", transport: "mcp_remote", authKind: "oauth",
      status: "draft", enabled: false,
      config: { sourceTemplateKey: body.galleryKey, connectionMethodKey: "mcp-oauth", url: "https://huggingface.co/mcp" }
    };
    connections.set(connection.id, connection);
    return send({ connectionId: connection.id, connection }, 201);
  }
  const id = p.startsWith("/api/tool-connections/") ? p.split("/")[3] : null;
  if (id && connections.has(id) && p === `/api/tool-connections/${id}`) {
    if (req.method === "GET") return send(connections.get(id));
    if (req.method === "PATCH") {
      events.push(`refused update ${id}`);
      return send({ error: "Template connections cannot be updated in place" }, 422);
    }
    if (req.method === "DELETE") {
      events.push(`archive ${id}`);
      connections.get(id).status = "archived";
      return send({ success: true });
    }
  }
  return false;
};

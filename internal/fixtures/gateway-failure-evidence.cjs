// Preserve complete, sanitized fixture evidence at the original failed phase.
const fs = require("fs");
const redact = text => text.replace(/pcgw_[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)?/g, "[REDACTED_GATEWAY_TOKEN]");
const files = fs.readdirSync(".").filter(name => /\.(log|json)$|tfstate/.test(name));
const phase = process.argv[2];
for (const file of files) {
  const safe = redact(fs.readFileSync(file,"utf8"));
  process.stderr.write(JSON.stringify({timestamp:new Date().toISOString(),time_unix_nano:String(BigInt(Date.now())*1000000n),level:"error",service:"provider-gateway-fixture",action:"failure_evidence",message:"Gateway OpenTofu phase failed",attributes:{phase,file,bytes:Buffer.byteLength(safe),complete:true,content:safe},error:{type:"FixturePhaseFailure",message:phase,traceback:"see retained original command output"}})+"\n");
}

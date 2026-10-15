// Test-owned boundary for the pinned native profile/binding/policy API.
// It is loaded only by the parent provider-opentofu-test derivation.
const ownedProfiles = [];
const events = [];
let policySequence = 0;
module.exports = function toolAccess(req, res, body, state) {
  const p = new URL(req.url, "http://fixture").pathname;
  const send = (value, status = 200) => { res.writeHead(status); res.end(JSON.stringify(value)); return true; };
  const fail = (error, status = 422) => send({ error }, status);
  const record = () => events.push({ method: req.method, path: p, body });
  if (p === "/api/test/tool-access" && req.method === "GET") return send({profiles:state.profiles.concat(ownedProfiles),policies:Object.values(state.policies),events});
  if (p === "/api/test/tool-access-seed" && req.method === "POST") {
    ownedProfiles.push({id:"finish-profile",companyId:"comp-1",profileKey:"app:finish",name:"Finish",description:null,status:"active",defaultAction:"deny",metadata:{source:"app_gallery_finish"},entries:[],bindings:[]});
    state.policies["finish-policy"]={id:"finish-policy",companyId:"comp-1",name:"Finish",policyType:"require_approval",priority:15,enabled:true,selectors:{},config:{source:"app_gallery_finish"}};
    state.policies["foreign-policy"]={id:"foreign-policy",companyId:"comp-1",name:"Other owner",policyType:"block",priority:7,enabled:true,selectors:{}};
    return send({});
  }
  if (p === "/api/companies/comp-1/tools/policies") {
    if (req.method === "GET") return send({policies:Object.values(state.policies)});
    if (req.method === "POST") {
      if (!["allow","block","require_approval","rate_limit"].includes(body.policyType)) return fail("Unsupported generic policy type");
      if (body.policyType !== "rate_limit" && body.config && Object.keys(body.config).length) return fail("config is only supported for rate_limit");
      if (Object.values(state.policies).some(policy => policy.name === body.name)) return fail("Duplicate policy name",409);
      const policy={id:"pol-"+(++policySequence),companyId:"comp-1",name:body.name,description:body.description ?? null,policyType:body.policyType,priority:body.priority ?? 100,enabled:body.enabled ?? true,selectors:body.selectors ?? {},conditions:body.conditions ?? null,config:body.config ?? null};
      state.policies[policy.id]=policy;record();return send(policy,201);
    }
  }
  const policyMatch=p.match(/^\/api\/companies\/comp-1\/tools\/policies\/([^/]+)$/);
  if (policyMatch && ["PATCH","DELETE"].includes(req.method)) {
    const policy=state.policies[policyMatch[1]];
    if (!policy || policy.policyType === "trust_rule") return fail("Tool policy not found",404);
    record();
    if (req.method === "DELETE") {delete state.policies[policy.id];return send(policy);}
    for (const key of ["name","description","policyType","priority","enabled","selectors","conditions","config"]) if (body[key] !== undefined) policy[key]=body[key];
    return send(policy);
  }
  if (p === "/api/companies/comp-1/tools/profiles") {
    if (req.method === "GET") return send({profiles:state.profiles.concat(ownedProfiles).map(profile=>({...profile,summary:{isCompanyDefault:profile.bindings.some(binding=>binding.targetType === "company" && binding.targetId === "comp-1")}}))});
    if (req.method === "POST") {
      if (state.profiles.concat(ownedProfiles).some(profile=>profile.profileKey === body.profileKey)) return fail("Duplicate profile key",409);
      const profile={id:"profile-"+(ownedProfiles.length+1),companyId:"comp-1",profileKey:body.profileKey,name:body.name,description:body.description ?? null,status:body.status ?? "active",defaultAction:body.defaultAction ?? "deny",metadata:body.metadata ?? {},entries:body.entries ?? [],bindings:[]};
      ownedProfiles.push(profile);record();return send(profile,201);
    }
  }
  const profileMatch=p.match(/^\/api\/tool-profiles\/([^/]+)$/);
  if (profileMatch && ["PATCH","DELETE"].includes(req.method)) {
    const profile=ownedProfiles.find(item=>item.id === profileMatch[1]);
    if (!profile) return fail("Tool profile not found",404);
    if (req.method === "PATCH") {
      for (const key of ["profileKey","name","description","status","defaultAction","metadata","entries"]) if (body[key] !== undefined && body[key] !== null) profile[key]=body[key];
    } else {
      if (profile.bindings.some(binding=>binding.targetType === "company" && binding.targetId === "comp-1") && !body.force && !body.reassignToProfileId) return fail("Cannot delete the company default tool profile");
      if (body.reassignToProfileId) {
        const target=ownedProfiles.find(item=>item.id === body.reassignToProfileId);
        if (!target || target === profile || target.status !== "active") return fail("Reassignment requires a different active profile");
        for (const binding of profile.bindings) if (!target.bindings.some(item=>item.targetType === binding.targetType && item.targetId === binding.targetId)) target.bindings.push({...binding,profileId:target.id});
      }
      ownedProfiles.splice(ownedProfiles.indexOf(profile),1);
    }
    record();return send(profile);
  }
  const bindingMatch=p.match(/^\/api\/companies\/comp-1\/tools\/profiles\/([^/]+)\/(bind|unbind)$/);
  if (bindingMatch && req.method === "POST") {
    const profile=ownedProfiles.find(item=>item.id === bindingMatch[1]);
    // The older app_permission fixture owns its separate finish-route profile.
    if (!profile) return false;
    const targets={agent:state.agents,project:state.projects};
    if (body.targetType === "company" ? body.targetId !== "comp-1" : !targets[body.targetType]?.[body.targetId]) return fail("Binding target must belong to the same company");
    const existing=profile.bindings.find(item=>item.targetType === body.targetType && item.targetId === body.targetId);
    if (bindingMatch[2] === "bind") {
      if (existing) return fail("Duplicate profile binding",409);
      const binding={id:"binding-"+profile.bindings.length,companyId:"comp-1",profileId:profile.id,targetType:body.targetType,targetId:body.targetId,priority:body.priority ?? 100,metadata:body.metadata ?? {}};
      profile.bindings.push(binding);record();return send(binding,201);
    }
    profile.bindings=profile.bindings.filter(item=>item !== existing);record();return send({unbound:existing ? 1:0});
  }
  return false;
};

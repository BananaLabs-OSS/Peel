local M = {}

local function call(cell, provider, payload)
  return pulp.unpack(pulp.call_raw(cell, provider, pulp.pack(payload)))
end

function M.health(_)
  return { status = "healthy" }
end

local function source_ip(endpoint)
  local ipv6 = string.match(endpoint, "^%[([^%]]+)%]:")
  if ipv6 ~= nil then return ipv6 end
  return string.match(endpoint, "^(.*):[^:]+$") or endpoint
end

function M.route_resolve(payload)
	if payload.join_token ~= nil and payload.join_token ~= "" then
		local resolve_url = string.gsub(payload.resolver_url, "/route%-request$", "/connections/resolve")
		local resolved = call("http-json", "engine.http-json.v1.request", {
			method = "POST", url = resolve_url,
			headers = { ["Content-Type"] = "application/json" },
			body = { connection_id = payload.session_key, lease_token = payload.join_token, transport = payload.transport, source_endpoint = payload.source_endpoint },
			timeout_ms = 5000,
		})
		if resolved.status ~= 200 or resolved.value == nil or resolved.value.backend == nil then
			error("join lease resolution failed")
		end
		return { key = payload.session_key, target = resolved.value.backend }
	end
  local key = source_ip(payload.source_endpoint)
  local existing = call("routing-state", "routing.state.v1.route.get", {
    key = key,
  })
  if existing.found == true then
    return { key = key, target = existing.route.target }
  end

  local blocked = call("routing-state", "routing.state.v1.negative.check", {
    key = key, now = tostring(payload.now_millis),
  })
  if blocked.blocked == true then error("route resolution temporarily suppressed") end

  local response = call("http-json", "engine.http-json.v1.request", {
    method = "POST",
    url = payload.resolver_url,
    headers = { ["Content-Type"] = "application/json" },
    body = { player_ip = key },
    timeout_ms = 5000,
  })
  if response.status ~= 200 or response.value == nil or response.value.backend == nil then
    call("routing-state", "routing.state.v1.negative.record", {
      id = "negative:" .. payload.session_key,
      key = key,
      expires_at = tostring(payload.now_millis + 30000),
    })
    error("route resolution failed")
  end
  call("routing-state", "routing.state.v1.negative.clear", {
    id = "negative-clear:" .. payload.session_key, key = key,
  })
  call("routing-state", "routing.state.v1.route.set", {
    id = "route-resolve:" .. payload.session_key,
    key = key,
    target = response.value.backend,
  })
  return { key = key, target = response.value.backend }
end

function M.route_set(payload)
  local result = call("routing-state", "routing.state.v1.route.set", {
    id = payload.id, key = payload.player_ip, target = payload.backend,
  })
  if result.had_route == true and result.changed == true then
    call("routing-state", "routing.state.v1.session.close", {
      id = payload.id .. ":session",
      key = payload.player_ip,
    })
  end
  return { status = "ok" }
end

function M.route_delete(payload)
  call("routing-state", "routing.state.v1.route.delete", { id = payload.id, key = payload.player_ip })
  call("routing-state", "routing.state.v1.session.close", {
    id = payload.id .. ":session",
    key = payload.player_ip,
  })
  return { status = "ok" }
end

function M.session_close(payload)
  call("routing-state", "routing.state.v1.session.close", { id = payload.id, key = payload.player_ip })
  return { status = "ok" }
end

function M.route_list(_)
  local routes = call("routing-state", "routing.state.v1.route.list", {}).routes
  return { routes = routes }
end

function M.admission_issue(payload)
  return call("edge-admission", "edge.admission.v1.issue", payload)
end

function M.admission_consume(payload)
  return call("edge-admission", "edge.admission.v1.consume", payload)
end

function M.admission_revoke(payload)
  return call("edge-admission", "edge.admission.v1.revoke", payload)
end

function M.join_lease_issue(payload)
	local response = call("http-json", "engine.http-json.v1.request", {
		method = "POST", url = payload.issuer_url,
		headers = { ["Content-Type"] = "application/json" },
		body = { principal_id = payload.principal_id, device_id = payload.device_id, destination_id = payload.destination_id, fallback_destination_id = payload.fallback_destination_id, ttl_seconds = payload.ttl_seconds },
		timeout_ms = 5000,
	})
	if response.status ~= 200 and response.status ~= 201 then error("join lease issue failed") end
	return response.value
end

local EVENTS = {
  ["route.resolve.v1"] = M.route_resolve,
  ["peel.route.resolve.v1"] = M.route_resolve,
  ["peel.http.health.v1"] = M.health,
  ["peel.http.route.set.v1"] = M.route_set,
  ["peel.http.route.delete.v1"] = M.route_delete,
  ["peel.http.session.close.v1"] = M.session_close,
  ["peel.http.route.list.v1"] = M.route_list,
  ["edge.admission.issue.v1"] = M.admission_issue,
  ["edge.admission.consume.v1"] = M.admission_consume,
  ["edge.admission.revoke.v1"] = M.admission_revoke,
  ["peel.http.join-lease.issue.v1"] = M.join_lease_issue,
}

for event, handler in pairs(EVENTS) do
  pulp.on(event, handler)
end

M.events = EVENTS
return M

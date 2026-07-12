// Types for the Loopback Gateway MCP tool groups governance UI. These mirror
// the backend transports/bifrost-http/handlers/mcptoolgroups.go contract and
// the configstore TableMCPToolGroup / MCPToolRef shapes.

// MCPToolGroupScope mirrors the backend scope enum. A tool group is primarily
// bound to a virtual key or team, but global and customer scopes are accepted
// for parity with the other governance entities.
export type MCPToolGroupScope = "global" | "virtual_key" | "team" | "customer";

// MCPToolRef is a single reference to an MCP tool inside a group: the MCP
// client (server) that owns the tool plus the tool's name on that client.
export interface MCPToolRef {
	client_id: string;
	tool_name: string;
}

// MCPToolGroup is a named, scoped collection of MCP tool references.
export interface MCPToolGroup {
	id: string;
	name: string;
	description?: string;
	enabled: boolean;
	scope: MCPToolGroupScope;
	scope_id?: string | null;
	scope_name?: string;
	tools: MCPToolRef[];
	created_at: string;
	updated_at: string;
}

export interface ListMCPToolGroupsResponse {
	tool_groups: MCPToolGroup[];
	total: number;
	count: number;
	offset: number;
}

export interface MCPToolGroupResponse {
	tool_group: MCPToolGroup;
	message?: string;
}

export interface ListMCPToolGroupsParams {
	scope?: MCPToolGroupScope;
	scope_id?: string;
	search?: string;
	limit?: number;
	offset?: number;
}

export interface CreateMCPToolGroupRequest {
	name: string;
	description?: string;
	enabled?: boolean;
	scope?: MCPToolGroupScope;
	scope_id?: string | null;
	tools: MCPToolRef[];
}

export interface UpdateMCPToolGroupRequest {
	id: string;
	name?: string;
	description?: string;
	enabled?: boolean;
	tools?: MCPToolRef[];
}
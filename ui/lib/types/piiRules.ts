// Types for the Loopback Gateway PII redactor UI. These mirror the backend
// transports/bifrost-http/handlers/pii_rules.go contract and the configstore
// TablePIIRule shape.

// PIIRuleScope mirrors the backend scope enum (identical to guardrail scopes).
export type PIIRuleScope = "global" | "virtual_key" | "team" | "customer";

// PIIRuleType is the rule discriminator. "regex" drives an in-process Redactor
// rule; "presidio" drives a Presidio analyzer text transformer.
export type PIIRuleType = "regex" | "presidio";

// PIIRule is a single, scoped PII redaction rule.
export interface PIIRule {
	id: string;
	name: string;
	description?: string;
	type: PIIRuleType;
	enabled: boolean;
	scope: PIIRuleScope;
	scope_id?: string | null;
	scope_name?: string;
	order: number;

	// regex discriminator fields
	regex_pattern?: string;
	regex_replacement?: string;

	// presidio discriminator fields
	presidio_base_url?: string;
	presidio_entity_type?: string;
	presidio_score_threshold?: number;

	created_at: string;
	updated_at: string;
}

export interface ListPIIRulesResponse {
	pii_rules: PIIRule[];
	total: number;
	count: number;
	offset: number;
}

export interface PIIRuleResponse {
	pii_rule: PIIRule;
	message?: string;
}

export interface ListPIIRulesParams {
	scope?: PIIRuleScope;
	scope_id?: string;
	type?: PIIRuleType;
	search?: string;
	limit?: number;
	offset?: number;
}

export interface CreatePIIRuleRequest {
	name: string;
	description?: string;
	type: PIIRuleType;
	enabled?: boolean;
	scope?: PIIRuleScope;
	scope_id?: string | null;
	order?: number;

	regex_pattern?: string;
	regex_replacement?: string;

	presidio_base_url?: string;
	presidio_entity_type?: string;
	presidio_score_threshold?: number;
}

export interface UpdatePIIRuleRequest {
	id: string;
	name?: string;
	description?: string;
	enabled?: boolean;
	order?: number;

	regex_pattern?: string;
	regex_replacement?: string;

	presidio_base_url?: string;
	presidio_entity_type?: string;
	presidio_score_threshold?: number;
}

export interface TestPIIRuleRequest {
	id: string;
	text: string;
}

export interface TestPIIRuleResponse {
	input: string;
	redacted: string;
	matched: boolean;
	type: PIIRuleType;
	warning?: string;
}

// PII_RULE_TYPES drives the create/edit form's type selector.
export const PII_RULE_TYPES: { value: PIIRuleType; label: string; description: string }[] = [
	{
		value: "regex",
		label: "Regex",
		description: "Mask every match of a regular expression with a replacement string. Fast and dependency-free.",
	},
	{
		value: "presidio",
		label: "Presidio",
		description: "Detect free-text PII (names, locations, etc.) via a self-hosted Microsoft Presidio analyzer.",
	},
];
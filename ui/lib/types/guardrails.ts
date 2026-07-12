// Types for the Loopback Gateway guardrails configuration UI. These mirror the
// backend transports/bifrost-http/handlers/guardrails.go contract and the
// configstore TableGuardrailConfig / GuardrailItem shapes.

// GuardrailScope mirrors the backend scope enum.
export type GuardrailScope = "global" | "virtual_key" | "team" | "customer";

// GuardrailType is one of the 15 supported guardrail types. The canonical
// identifier is the lowerCamel form the loopback-guard plugin reports.
export type GuardrailType =
	| "contains"
	| "regexMatch"
	| "wordCount"
	| "characterCount"
	| "sentenceCount"
	| "endsWith"
	| "allLowercase"
	| "allUppercase"
	| "notNull"
	| "containsCode"
	| "validUrls"
	| "validJson"
	| "jsonKeys"
	| "modelWhitelist"
	| "allowedRequestTypes";

// GuardrailParams is the polymorphic, type-specific parameter bag. Each key is
// only meaningful for some guardrail types (see GUARDRAIL_DEFINITIONS).
export interface GuardrailParams {
	words?: string[];
	keys?: string[];
	models?: string[];
	allowed?: string[];
	blocked?: string[];
	operator?: "any" | "all" | "none";
	pattern?: string;
	suffix?: string;
	format?: string;
	min?: number;
	max?: number;
	not?: boolean;
	caseInsensitive?: boolean;
}

// GuardrailItem is a single guardrail inside a config.
export interface GuardrailItem {
	id?: string;
	type: GuardrailType;
	enabled: boolean;
	params?: GuardrailParams;
}

// GuardrailConfig is a named, scoped collection of guardrails.
export interface GuardrailConfig {
	id: string;
	name: string;
	enabled: boolean;
	scope: GuardrailScope;
	scope_id?: string | null;
	scope_name?: string;
	guardrails: GuardrailItem[];
	created_at: string;
	updated_at: string;
}

export interface ListGuardrailsResponse {
	guardrails: GuardrailConfig[];
	total: number;
	count: number;
	offset: number;
}

export interface GuardrailConfigResponse {
	guardrail_config: GuardrailConfig;
	message?: string;
}

export interface ListGuardrailsParams {
	scope?: GuardrailScope;
	scope_id?: string;
	search?: string;
	limit?: number;
	offset?: number;
}

export interface CreateGuardrailRequest {
	name: string;
	enabled?: boolean;
	scope?: GuardrailScope;
	scope_id?: string | null;
	guardrails: GuardrailItem[];
}

export interface UpdateGuardrailRequest {
	id: string;
	name?: string;
	enabled?: boolean;
	guardrails?: GuardrailItem[];
}

// ParamField describes a single editable parameter for a guardrail type, so the
// UI can render a generic form per type.
export interface ParamField {
	key: keyof GuardrailParams;
	label: string;
	kind: "stringList" | "operator" | "text" | "number" | "boolean";
	placeholder?: string;
}

// GuardrailDefinition is the UI metadata for a guardrail type.
export interface GuardrailDefinition {
	type: GuardrailType;
	label: string;
	description: string;
	// scope tells the user whether the check runs over the request text or the
	// request envelope (model/request-type).
	target: "text" | "request";
	fields: ParamField[];
}

const NOT_FIELD: ParamField = { key: "not", label: "Invert (block when it matches)", kind: "boolean" };

// GUARDRAIL_DEFINITIONS drives the create/edit form. The 15 supported types.
export const GUARDRAIL_DEFINITIONS: GuardrailDefinition[] = [
	{
		type: "contains",
		label: "Contains words",
		description: "Match against a list of words/substrings. Use the 'none' operator for a blocklist.",
		target: "text",
		fields: [
			{ key: "words", label: "Words", kind: "stringList", placeholder: "Add a word and press Enter" },
			{ key: "operator", label: "Operator", kind: "operator" },
			{ key: "caseInsensitive", label: "Case insensitive", kind: "boolean" },
		],
	},
	{
		type: "regexMatch",
		label: "Regex match",
		description: "Pass when the input matches the regular expression.",
		target: "text",
		fields: [{ key: "pattern", label: "Pattern", kind: "text", placeholder: "^[A-Z].*$" }, NOT_FIELD],
	},
	{
		type: "wordCount",
		label: "Word count",
		description: "Pass when the word count is within [min, max].",
		target: "text",
		fields: [{ key: "min", label: "Min", kind: "number" }, { key: "max", label: "Max", kind: "number" }, NOT_FIELD],
	},
	{
		type: "characterCount",
		label: "Character count",
		description: "Pass when the character count is within [min, max].",
		target: "text",
		fields: [{ key: "min", label: "Min", kind: "number" }, { key: "max", label: "Max", kind: "number" }, NOT_FIELD],
	},
	{
		type: "sentenceCount",
		label: "Sentence count",
		description: "Pass when the sentence count is within [min, max].",
		target: "text",
		fields: [{ key: "min", label: "Min", kind: "number" }, { key: "max", label: "Max", kind: "number" }, NOT_FIELD],
	},
	{
		type: "endsWith",
		label: "Ends with",
		description: "Pass when the input ends with the given suffix.",
		target: "text",
		fields: [{ key: "suffix", label: "Suffix", kind: "text", placeholder: "." }, NOT_FIELD],
	},
	{
		type: "allLowercase",
		label: "All lowercase",
		description: "Pass when every alphabetic character is lowercase.",
		target: "text",
		fields: [NOT_FIELD],
	},
	{
		type: "allUppercase",
		label: "All uppercase",
		description: "Pass when every alphabetic character is uppercase.",
		target: "text",
		fields: [NOT_FIELD],
	},
	{
		type: "notNull",
		label: "Not null",
		description: "Pass when the input is non-empty after trimming whitespace.",
		target: "text",
		fields: [NOT_FIELD],
	},
	{
		type: "containsCode",
		label: "Contains code",
		description: "Pass when a fenced code block of the given language is present.",
		target: "text",
		fields: [{ key: "format", label: "Language", kind: "text", placeholder: "Python" }, NOT_FIELD],
	},
	{
		type: "validUrls",
		label: "Valid URLs",
		description: "Pass when every http(s) URL found in the input is syntactically valid.",
		target: "text",
		fields: [NOT_FIELD],
	},
	{
		type: "validJson",
		label: "Valid JSON",
		description: "Pass when the whole input parses as JSON.",
		target: "text",
		fields: [NOT_FIELD],
	},
	{
		type: "jsonKeys",
		label: "JSON keys",
		description: "Check that JSON found in the input contains the configured keys.",
		target: "text",
		fields: [
			{ key: "keys", label: "Keys", kind: "stringList", placeholder: "Add a key and press Enter" },
			{ key: "operator", label: "Operator", kind: "operator" },
		],
	},
	{
		type: "modelWhitelist",
		label: "Model whitelist",
		description: "Pass when the requested model is in the allowed list.",
		target: "request",
		fields: [{ key: "models", label: "Models", kind: "stringList", placeholder: "Add a model and press Enter" }, NOT_FIELD],
	},
	{
		type: "allowedRequestTypes",
		label: "Allowed request types",
		description: "Allow/deny by request type (e.g. chat, embedding).",
		target: "request",
		fields: [
			{ key: "allowed", label: "Allowed types", kind: "stringList", placeholder: "chat" },
			{ key: "blocked", label: "Blocked types", kind: "stringList", placeholder: "embedding" },
		],
	},
];

export function guardrailDefinition(type: GuardrailType): GuardrailDefinition | undefined {
	return GUARDRAIL_DEFINITIONS.find((d) => d.type === type);
}
// Types for the Loopback Gateway JWT auth UI (data-plane external-IdP JWTs
// mapped to virtual keys). Mirrors the backend
// transports/bifrost-http/handlers/jwtauth.go contract and the configstore
// TableJWTAuthConfig shape. Virtual keys are referenced by ID only — VK values
// never travel through this API.

// JWTAuthClaimMapping is one claim→virtual-key mapping rule; first match wins.
export interface JWTAuthClaimMapping {
	claim: string;
	value: string; // exact value, or "*" for any present value
	virtual_key_id: string;
}

// JWTAuthConfig is one trusted external issuer.
export interface JWTAuthConfig {
	id: string;
	name: string;
	enabled: boolean;
	issuer: string;
	jwks_url: string;
	audience: string;
	reject_invalid: boolean;
	claim_mappings: JWTAuthClaimMapping[] | null;
	default_virtual_key_id: string;
	created_at: string;
	updated_at: string;
}

// JWTAuthConfigInput is the create/update payload; omitted fields are left
// unchanged on update.
export interface JWTAuthConfigInput {
	name?: string;
	enabled?: boolean;
	issuer?: string;
	jwks_url?: string;
	audience?: string;
	reject_invalid?: boolean;
	claim_mappings?: JWTAuthClaimMapping[];
	default_virtual_key_id?: string;
}
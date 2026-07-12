// JWT auth management view: trusted external JWT issuers
// whose tokens are mapped to virtual keys on the LLM data plane. Backed by
// the RTK-query slice in @/lib/store/apis/jwtAuthApi.
//
// The feature is OPT-IN / default-off on the backend: with zero enabled
// issuer rows the inference path is unchanged. Virtual keys are selected by
// ID from the existing virtual-keys list — their values never appear here.
import { useState } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage, useGetVirtualKeysQuery } from "@/lib/store";
import {
	useCreateJWTAuthConfigMutation,
	useDeleteJWTAuthConfigMutation,
	useGetJWTAuthConfigsQuery,
	useUpdateJWTAuthConfigMutation,
} from "@/lib/store/apis/jwtAuthApi";
import { JWTAuthClaimMapping, JWTAuthConfig, JWTAuthConfigInput } from "@/lib/types/jwtAuth";

// NO_VK is the Radix Select sentinel for "no default virtual key" (empty item
// values are not allowed); mapped to "" before submitting.
const NO_VK = "__none__";

interface JWTAuthFormState {
	name: string;
	issuer: string;
	jwks_url: string;
	audience: string;
	reject_invalid: boolean;
	claim_mappings: JWTAuthClaimMapping[];
	default_virtual_key_id: string;
}

const emptyForm: JWTAuthFormState = {
	name: "",
	issuer: "",
	jwks_url: "",
	audience: "",
	reject_invalid: false,
	claim_mappings: [],
	default_virtual_key_id: "",
};

export default function JWTAuthView() {
	const { data, isLoading, isError, error } = useGetJWTAuthConfigsQuery();
	const { data: vkData } = useGetVirtualKeysQuery();
	const [createConfig, { isLoading: isCreating }] = useCreateJWTAuthConfigMutation();
	const [updateConfig, { isLoading: isUpdating }] = useUpdateJWTAuthConfigMutation();
	const [deleteConfig] = useDeleteJWTAuthConfigMutation();

	const [editingID, setEditingID] = useState("");
	const [showForm, setShowForm] = useState(false);
	const [form, setForm] = useState<JWTAuthFormState>({ ...emptyForm });

	const configs = data?.jwt_auth_configs ?? [];
	const virtualKeys = vkData?.virtual_keys ?? [];

	const vkName = (id: string) => virtualKeys.find((vk) => vk.id === id)?.name ?? id;

	const startCreate = () => {
		setEditingID("");
		setForm({ ...emptyForm });
		setShowForm(true);
	};

	const startEdit = (config: JWTAuthConfig) => {
		setEditingID(config.id);
		setForm({
			name: config.name,
			issuer: config.issuer,
			jwks_url: config.jwks_url,
			audience: config.audience,
			reject_invalid: config.reject_invalid,
			claim_mappings: config.claim_mappings ?? [],
			default_virtual_key_id: config.default_virtual_key_id,
		});
		setShowForm(true);
	};

	const setMapping = (index: number, patch: Partial<JWTAuthClaimMapping>) => {
		setForm((f) => ({
			...f,
			claim_mappings: f.claim_mappings.map((m, i) => (i === index ? { ...m, ...patch } : m)),
		}));
	};

	const handleSubmit = async () => {
		const body: JWTAuthConfigInput = { ...form };
		try {
			if (editingID === "") {
				await createConfig(body).unwrap();
				toast.success("JWT auth config created.");
			} else {
				await updateConfig({ id: editingID, body }).unwrap();
				toast.success("JWT auth config updated.");
			}
			setShowForm(false);
			setForm({ ...emptyForm });
			setEditingID("");
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const handleToggleEnabled = async (config: JWTAuthConfig, enabled: boolean) => {
		try {
			await updateConfig({ id: config.id, body: { enabled } }).unwrap();
			toast.success(enabled ? "Issuer enabled." : "Issuer disabled.");
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const handleDelete = async (config: JWTAuthConfig) => {
		try {
			await deleteConfig(config.id).unwrap();
			toast.success(`Issuer "${config.issuer}" deleted.`);
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	return (
		<div className="w-full space-y-4">
			<div className="flex items-start justify-between gap-4">
				<div>
					<h2 className="text-lg font-semibold tracking-tight">JWT Authentication</h2>
					<p className="text-muted-foreground text-sm">
						Let callers authenticate inference requests with their identity provider's JWTs. Verified claims map to virtual keys, so
						budgets, rate limits, and team attribution apply without distributing gateway keys.
					</p>
				</div>
				<Button onClick={startCreate} data-testid="jwt-auth-create-button">
					<Plus className="mr-1 h-4 w-4" /> Add issuer
				</Button>
			</div>

			{showForm && (
				<Card data-testid="jwt-auth-form">
					<CardHeader>
						<CardTitle className="text-base">{editingID === "" ? "New trusted issuer" : "Edit trusted issuer"}</CardTitle>
						<CardDescription>
							Tokens must be RS256/384/512-signed by a key from the JWKS endpoint, carry this exact issuer, and (when set) the audience.
							Mapping rules are evaluated top to bottom; the first match assigns the virtual key.
						</CardDescription>
					</CardHeader>
					<CardContent className="space-y-4">
						<div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
							<div className="flex flex-col gap-1">
								<Label htmlFor="jwt-auth-name">Name</Label>
								<Input
									id="jwt-auth-name"
									value={form.name}
									onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
									placeholder="corp-idp"
									data-testid="jwt-auth-name-input"
								/>
							</div>
							<div className="flex flex-col gap-1">
								<Label htmlFor="jwt-auth-issuer">Issuer</Label>
								<Input
									id="jwt-auth-issuer"
									value={form.issuer}
									onChange={(e) => setForm((f) => ({ ...f, issuer: e.target.value }))}
									placeholder="https://idp.example.com/realms/prod"
									data-testid="jwt-auth-issuer-input"
								/>
							</div>
							<div className="flex flex-col gap-1">
								<Label htmlFor="jwt-auth-jwks">JWKS URL</Label>
								<Input
									id="jwt-auth-jwks"
									value={form.jwks_url}
									onChange={(e) => setForm((f) => ({ ...f, jwks_url: e.target.value }))}
									placeholder="https://idp.example.com/.well-known/jwks.json"
									data-testid="jwt-auth-jwks-input"
								/>
							</div>
							<div className="flex flex-col gap-1">
								<Label htmlFor="jwt-auth-audience">Audience (optional)</Label>
								<Input
									id="jwt-auth-audience"
									value={form.audience}
									onChange={(e) => setForm((f) => ({ ...f, audience: e.target.value }))}
									placeholder="loopback-gateway"
									data-testid="jwt-auth-audience-input"
								/>
							</div>
						</div>

						<div className="flex items-center justify-between rounded-sm border p-3">
							<div className="space-y-0.5">
								<Label className="text-sm font-medium">Reject invalid tokens</Label>
								<p className="text-muted-foreground text-sm">
									Return 401 when a token from this issuer fails verification. Off = fall through, letting governance decide (requests
									without a mandatory virtual key are still rejected there).
								</p>
							</div>
							<Switch
								checked={form.reject_invalid}
								onCheckedChange={(checked) => setForm((f) => ({ ...f, reject_invalid: checked }))}
								data-testid="jwt-auth-reject-invalid-switch"
							/>
						</div>

						<div className="flex flex-col gap-2">
							<Label>Claim mappings (first match wins)</Label>
							{form.claim_mappings.map((mapping, index) => (
								<div key={index} className="flex flex-wrap items-center gap-2" data-testid={`jwt-auth-mapping-row-${index}`}>
									<Input
										className="w-56"
										value={mapping.claim}
										onChange={(e) => setMapping(index, { claim: e.target.value })}
										placeholder="claim path, e.g. tenant or realm_access.roles"
										data-testid={`jwt-auth-mapping-claim-${index}`}
									/>
									<Input
										className="w-40"
										value={mapping.value}
										onChange={(e) => setMapping(index, { value: e.target.value })}
										placeholder={`value ("*" = any)`}
										data-testid={`jwt-auth-mapping-value-${index}`}
									/>
									<Select
										value={mapping.virtual_key_id || NO_VK}
										onValueChange={(v) => setMapping(index, { virtual_key_id: v === NO_VK ? "" : v })}
									>
										<SelectTrigger className="w-56" data-testid={`jwt-auth-mapping-vk-${index}`}>
											<SelectValue placeholder="Virtual key" />
										</SelectTrigger>
										<SelectContent>
											<SelectItem value={NO_VK}>Select virtual key…</SelectItem>
											{virtualKeys.map((vk) => (
												<SelectItem key={vk.id} value={vk.id}>
													{vk.name}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
									<Button
										variant="ghost"
										size="sm"
										onClick={() => setForm((f) => ({ ...f, claim_mappings: f.claim_mappings.filter((_, i) => i !== index) }))}
										data-testid={`jwt-auth-mapping-remove-${index}`}
									>
										<Trash2 className="h-4 w-4" />
									</Button>
								</div>
							))}
							<Button
								variant="outline"
								size="sm"
								className="w-fit"
								onClick={() =>
									setForm((f) => ({ ...f, claim_mappings: [...f.claim_mappings, { claim: "", value: "", virtual_key_id: "" }] }))
								}
								data-testid="jwt-auth-mapping-add-button"
							>
								<Plus className="mr-1 h-4 w-4" /> Add mapping rule
							</Button>
						</div>

						<div className="flex flex-col gap-1">
							<Label>Default virtual key (used when no rule matches)</Label>
							<Select
								value={form.default_virtual_key_id || NO_VK}
								onValueChange={(v) => setForm((f) => ({ ...f, default_virtual_key_id: v === NO_VK ? "" : v }))}
							>
								<SelectTrigger className="w-64" data-testid="jwt-auth-default-vk-select">
									<SelectValue placeholder="None" />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value={NO_VK}>None</SelectItem>
									{virtualKeys.map((vk) => (
										<SelectItem key={vk.id} value={vk.id}>
											{vk.name}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>

						<div className="flex gap-2">
							<Button onClick={handleSubmit} disabled={isCreating || isUpdating} data-testid="jwt-auth-save-button">
								{editingID === "" ? "Create issuer" : "Save changes"}
							</Button>
							<Button variant="ghost" onClick={() => setShowForm(false)}>
								Cancel
							</Button>
						</div>
					</CardContent>
				</Card>
			)}

			{isLoading && <p className="text-muted-foreground text-sm">Loading JWT auth configs...</p>}
			{isError && <p className="text-sm text-red-500">Failed to load JWT auth configs: {getErrorMessage(error)}</p>}

			{!isLoading && !isError && (
				<div className="overflow-auto rounded-sm border">
					<Table data-testid="jwt-auth-table">
						<TableHeader>
							<TableRow className="bg-muted/50">
								<TableHead className="font-semibold">Issuer</TableHead>
								<TableHead className="font-semibold">Mappings</TableHead>
								<TableHead className="font-semibold">On invalid token</TableHead>
								<TableHead className="font-semibold">Enabled</TableHead>
								<TableHead className="text-right font-semibold">Actions</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{configs.length === 0 ? (
								<TableRow data-testid="jwt-auth-empty-state">
									<TableCell colSpan={5} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">
											No trusted issuers configured. JWT authentication is off until you add one.
										</span>
									</TableCell>
								</TableRow>
							) : (
								configs.map((config) => (
									<TableRow key={config.id} data-testid={`jwt-auth-row-${config.id}`}>
										<TableCell>
											<div className="font-medium">{config.name || config.issuer}</div>
											<div className="text-muted-foreground text-xs">{config.issuer}</div>
										</TableCell>
										<TableCell>
											<div className="flex flex-wrap gap-1">
												{(config.claim_mappings ?? []).map((m, i) => (
													<Badge key={i} variant="secondary" className="text-xs">
														{m.claim}={m.value} → {vkName(m.virtual_key_id)}
													</Badge>
												))}
												{config.default_virtual_key_id && (
													<Badge variant="outline" className="text-xs">
														default → {vkName(config.default_virtual_key_id)}
													</Badge>
												)}
												{(config.claim_mappings ?? []).length === 0 && !config.default_virtual_key_id && (
													<span className="text-muted-foreground text-sm">No mappings</span>
												)}
											</div>
										</TableCell>
										<TableCell>
											<Badge variant={config.reject_invalid ? "destructive" : "secondary"}>
												{config.reject_invalid ? "Reject (401)" : "Fall through"}
											</Badge>
										</TableCell>
										<TableCell>
											<Switch
												checked={config.enabled}
												onCheckedChange={(checked) => handleToggleEnabled(config, checked)}
												data-testid={`jwt-auth-enabled-switch-${config.id}`}
											/>
										</TableCell>
										<TableCell className="text-right">
											<div className="flex justify-end gap-1">
												<Button
													variant="ghost"
													size="sm"
													onClick={() => startEdit(config)}
													title="Edit"
													data-testid={`jwt-auth-edit-button-${config.id}`}
												>
													<Pencil className="h-4 w-4" />
												</Button>
												<Button
													variant="ghost"
													size="sm"
													onClick={() => handleDelete(config)}
													title="Delete"
													data-testid={`jwt-auth-delete-button-${config.id}`}
												>
													<Trash2 className="h-4 w-4" />
												</Button>
											</div>
										</TableCell>
									</TableRow>
								))
							)}
						</TableBody>
					</Table>
				</div>
			)}
		</div>
	);
}
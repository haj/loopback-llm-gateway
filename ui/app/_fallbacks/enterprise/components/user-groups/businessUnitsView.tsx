// Business unit management is now part of the open-source Loopback Gateway
// build. The @enterprise alias resolves here in OSS builds, so we re-export the
// real workspace view instead of an upsell stub.
export { BusinessUnitsView } from "@/app/workspace/governance/business-units/views/businessUnitsView";
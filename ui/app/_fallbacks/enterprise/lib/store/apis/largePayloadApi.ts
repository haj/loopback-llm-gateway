// OSS (Apache-2.0) large-payload streaming config API.
//
// This re-exports the open-source RTK-query slice that talks to the
// Loopback Gateway backend at `/api/large-payload-config`. It replaces the
// previous no-op stub so the settings panel reads and writes real values.
export { useGetLargePayloadConfigQuery, useUpdateLargePayloadConfigMutation } from "@/lib/store/apis/largePayloadApi";
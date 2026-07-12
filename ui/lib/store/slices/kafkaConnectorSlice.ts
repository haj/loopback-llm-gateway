import { createSlice, PayloadAction } from "@reduxjs/toolkit";

// The built-in plugin name the Kafka data connector registers under on the
// Loopback Gateway server (see plugins/loopbackkafka, PluginName).
export const KAFKA_CONNECTOR_PLUGIN = "loopback-kafka";

// KafkaConnectorForm is the editable surface of the connector config form.
// Brokers are edited as a single newline/comma-separated string and split into
// the string[] the backend expects on save.
export interface KafkaConnectorForm {
	enabled: boolean;
	brokers: string;
	topic: string;
}

export interface KafkaConnectorState {
	form: KafkaConnectorForm;
	isDirty: boolean;
}

const initialForm: KafkaConnectorForm = {
	enabled: false,
	brokers: "",
	topic: "",
};

const initialState: KafkaConnectorState = {
	form: initialForm,
	isDirty: false,
};

const kafkaConnectorSlice = createSlice({
	name: "kafkaConnector",
	initialState,
	reducers: {
		// Replace the whole form (e.g. when hydrating from the loaded plugin config).
		// Hydration is not a user edit, so it clears the dirty flag.
		setKafkaConnectorForm: (state, action: PayloadAction<KafkaConnectorForm>) => {
			state.form = action.payload;
			state.isDirty = false;
		},
		// Patch one or more fields from user input; marks the form dirty.
		updateKafkaConnectorForm: (state, action: PayloadAction<Partial<KafkaConnectorForm>>) => {
			state.form = { ...state.form, ...action.payload };
			state.isDirty = true;
		},
		setKafkaConnectorDirty: (state, action: PayloadAction<boolean>) => {
			state.isDirty = action.payload;
		},
		resetKafkaConnectorForm: (state) => {
			state.form = initialForm;
			state.isDirty = false;
		},
	},
});

export const { setKafkaConnectorForm, updateKafkaConnectorForm, setKafkaConnectorDirty, resetKafkaConnectorForm } =
	kafkaConnectorSlice.actions;

export default kafkaConnectorSlice.reducer;

// Selectors
export const selectKafkaConnectorForm = (state: { kafkaConnector: KafkaConnectorState }) => state.kafkaConnector.form;
export const selectKafkaConnectorIsDirty = (state: { kafkaConnector: KafkaConnectorState }) => state.kafkaConnector.isDirty;

// parseBrokers turns the textarea value into the broker string[] the backend wants,
// splitting on commas and newlines and dropping blanks.
export function parseBrokers(raw: string): string[] {
	return raw
		.split(/[\n,]+/)
		.map((b) => b.trim())
		.filter((b) => b.length > 0);
}
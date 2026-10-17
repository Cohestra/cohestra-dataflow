{{- define "dataflow.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "dataflow.appEnv" -}}
- name: DATABASE_URL
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: databaseUrl}}
- name: APP_DATABASE_URL
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: appDatabaseUrl}}
- name: REDIS_URL
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: redisUrl}}
- name: TEMPORAL_ADDRESS
  value: temporal:7233
- name: CLICKHOUSE_URL
  value: http://clickhouse:8123
- name: CLICKHOUSE_USER
  value: dataflow
- name: CLICKHOUSE_PASSWORD
  value: dataflow
- name: CLICKHOUSE_DB
  value: dataflow
- name: SMTP_HOST
  value: {{ .Values.smtp.host | quote }}
- name: SMTP_PORT
  value: {{ .Values.smtp.port | quote }}
- name: SMTP_FROM
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: smtpFrom, optional: true}}
- name: SMTP_USER
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: smtpUser, optional: true}}
- name: SMTP_PASS
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: smtpPass, optional: true}}
- name: APP_URL
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: appUrl}}
- name: JWT_ACCESS_SECRET
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: jwt}}
- name: OAUTH_TOKEN_ENCRYPTION_KEY
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: oauthKey}}
- name: TEMPORAL_PAYLOAD_ENCRYPTION_KEY
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: temporalPayloadKey}}
- name: NODE_ENV
  value: {{ ternary "production" "development" .Values.runtime.production | quote }}
- name: GOOGLE_CLIENT_ID
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: googleClientId, optional: true}}
- name: GOOGLE_CLIENT_SECRET
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: googleClientSecret, optional: true}}
- name: AZURE_CLIENT_ID
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: azureClientId, optional: true}}
- name: AZURE_CLIENT_SECRET
  valueFrom: {secretKeyRef: {name: dataflow-secrets, key: azureClientSecret, optional: true}}
- name: AUTH_PASSWORD_ENABLED
  value: "true"
- name: OLLAMA_URL
  value: http://ollama:11434
- name: OLLAMA_MODEL
  value: {{ .Values.ollamaModel | quote }}
- name: OLLAMA_MODELS
  value: {{ join "," .Values.ollamaModels | quote }}
- name: OLLAMA_NUM_CTX
  value: {{ .Values.ollama.numCtx | quote }}
- name: OLLAMA_TIMEOUT_SECONDS
  value: {{ .Values.ollama.timeoutSeconds | quote }}
- name: AI_FEW_SHOT
  value: {{ .Values.ollama.fewShot | quote }}
- name: COHESTRA_URL
  value: {{ .Values.cohestraURL | quote }}
- name: COHESTRA_FLINK_IMAGE
  value: dataflow-flink-sql@sha256:362650f23505dd07b809bf1f0d78c52fbc4e8ebaebd9204981672801b22c556d
- name: CONNECTORS_DIR
  value: /app/connectors/manifests
{{- end }}

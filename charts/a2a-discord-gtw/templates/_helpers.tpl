{{/*
Chart name, truncated to the 63 characters a DNS label allows.
*/}}
{{- define "a2a-discord-gtw.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Full name: the release name, prefixed to the chart name unless it already
contains it.
*/}}
{{- define "a2a-discord-gtw.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "a2a-discord-gtw.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "a2a-discord-gtw.labels" -}}
helm.sh/chart: {{ include "a2a-discord-gtw.chart" . }}
{{ include "a2a-discord-gtw.selectorLabels" . }}
app.kubernetes.io/version: {{ include "a2a-discord-gtw.imageTag" . | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "a2a-discord-gtw.selectorLabels" -}}
app.kubernetes.io/name: {{ include "a2a-discord-gtw.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "a2a-discord-gtw.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "a2a-discord-gtw.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "a2a-discord-gtw.imageTag" -}}
{{- default .Chart.AppVersion .Values.image.tag }}
{{- end }}

{{/*
Image reference: repository:tag, with @digest appended when one is given.
*/}}
{{- define "a2a-discord-gtw.image" -}}
{{- $ref := printf "%s:%s" .Values.image.repository (include "a2a-discord-gtw.imageTag" .) }}
{{- if .Values.image.digest }}
{{- $ref = printf "%s@%s" $ref .Values.image.digest }}
{{- end }}
{{- $ref }}
{{- end }}

{{/*
The Secret holding the Discord token. There is no way to pass the token itself.
*/}}
{{- define "a2a-discord-gtw.tokenSecret" -}}
{{- required "discordToken.existingSecret is required: create a Secret holding the bot token (kubectl create secret generic a2a-discord-gtw --from-literal=token=<bot token>) and set discordToken.existingSecret to its name. The chart never takes the token itself." .Values.discordToken.existingSecret }}
{{- end }}

{{- define "a2a-discord-gtw.claimName" -}}
{{- default (include "a2a-discord-gtw.fullname" .) .Values.persistence.existingClaim }}
{{- end }}

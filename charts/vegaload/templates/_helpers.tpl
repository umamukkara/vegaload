{{- define "vegaload.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "vegaload.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 50 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 50 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "vegaload.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "vegaload.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "vegaload.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{/*
The host of run.target, for -allow-target. A target may be a URL
(http://host:8080/x) or a bare host:port, so a bare one gets a scheme first.
*/}}
{{- define "vegaload.targetHost" -}}
{{- $t := .Values.run.target -}}
{{- if not (contains "://" $t) -}}{{- $t = printf "scheme://%s" $t -}}{{- end -}}
{{- (urlParse $t).hostname -}}
{{- end -}}

{{/*
The arguments for `vegaload`, one per line as a YAML list. The same flags a
person would type: see `vegaload run -h`. -audit-log goes to /tmp, the only
folder the pod can write to. -no-report: the summary in the log is the result.
*/}}
{{- define "vegaload.args" -}}
{{- $r := .Values.run -}}
{{- if and .Values.scenario (or $r.target $r.protocol) -}}
{{- fail "Set a scenario (--set-file scenario=...) or run.target with run.protocol, not both." -}}
{{- end -}}
{{- if and (not .Values.scenario) (or (not $r.target) (not $r.protocol)) -}}
{{- fail "Nothing to run. Set run.target and run.protocol, or give a scenario with --set-file scenario=./your.vl.js" -}}
{{- end -}}
- run
- -no-open
- -no-report
- -trigger
- k8s
- -audit-log
- /tmp/vegaload-audit.log
{{- with $r.executor }}
- -executor
- {{ . | quote }}
{{- end }}
{{- if gt (int $r.vus) 0 }}
- -vus
- {{ int $r.vus | quote }}
{{- end }}
{{- with $r.duration }}
- -duration
- {{ . | quote }}
{{- end }}
{{- with $r.stages }}
- -stages
- {{ . | quote }}
{{- end }}
{{- if gt (float64 $r.rate) 0.0 }}
- -rate
- {{ $r.rate | quote }}
{{- end }}
{{- if gt (int $r.maxVUs) 0 }}
- -max-vus
- {{ int $r.maxVUs | quote }}
{{- end }}
{{- range $r.thresholds }}
- -threshold
- {{ . | quote }}
{{- end }}
{{- $allow := $r.allowTargets }}
{{- if $r.target }}{{- $allow = append $allow (include "vegaload.targetHost" $) }}{{- end }}
{{- range ($allow | uniq | sortAlpha) }}
- -allow-target
- {{ . | quote }}
{{- end }}
{{- if $r.target }}
- -target
- {{ $r.target | quote }}
- -protocol
- {{ $r.protocol | quote }}
{{- else }}
- {{ printf "/scenario/%s" .Values.scenarioFileName | quote }}
{{- end }}
{{- end -}}

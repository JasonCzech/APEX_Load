#!/bin/bash
awk '
  /^const BASE =/ { skip=1; print "import { request, ApiError, BASE } from \"./apiClient\"" }
  skip && /^export function useApi/ { skip=0; print }
  skip { next }
  { print }
' src/hooks/useApi.ts > temp.ts && mv temp.ts src/hooks/useApi.ts

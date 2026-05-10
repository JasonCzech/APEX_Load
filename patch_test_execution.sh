#!/bin/bash
sed -i '' "18a\\
import { parseDurationToSeconds, formatTimeLong, formatTimeCompact } from '../utils/timeUtils'\\
import { downloadBlob } from '../utils/fileUtils'
" dashboard/src/components/TestExecution.tsx

# Delete saveBlob
sed -i '' '234,243d' dashboard/src/components/TestExecution.tsx

# Replace names
sed -i '' 's/saveBlob/downloadBlob/g' dashboard/src/components/TestExecution.tsx

# Delete time functions
sed -i '' '27,67d' dashboard/src/components/TestExecution.tsx

# Replace formatPreviewTime with formatTimeLong (actually formatPreviewTime(0) returns 'now', but formatTimeLong doesn't. We should see if the difference matters).
sed -i '' 's/formatPreviewTime(/formatTimeLong(/g' dashboard/src/components/TestExecution.tsx
sed -i '' 's/formatScenarioTimeline(/formatTimeCompact(/g' dashboard/src/components/TestExecution.tsx

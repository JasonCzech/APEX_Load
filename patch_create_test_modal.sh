#!/bin/bash
sed -i '' "5a\\
import { parseDurationToSeconds, formatTimeCompact } from '../utils/timeUtils'
" dashboard/src/components/CreateTestModal.tsx

# Delete parseDuration and formatTime
sed -i '' '8,31d' dashboard/src/components/CreateTestModal.tsx

# Replace names
sed -i '' 's/parseDuration(/parseDurationToSeconds(/g' dashboard/src/components/CreateTestModal.tsx
sed -i '' 's/formatTime(/formatTimeCompact(/g' dashboard/src/components/CreateTestModal.tsx

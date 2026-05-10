#!/bin/bash
sed -i '' 's/import React, { useState, useEffect, useRef } from '"'"'react'"'"'/import React, { useState, useEffect, useRef } from '"'"'react'"'"'\nimport { downloadBlob } from '"'"'..\/utils\/fileUtils'"'"'/g' dashboard/src/components/RecordingStudio.tsx

cat << 'INNER' > replacement.txt
  const handleDownloadCA = async (format: 'crt' | 'pem' = 'crt') => {
    try {
      const url = format === 'pem' ? '/api/v1/recorder/ca.pem?format=pem' : '/api/v1/recorder/ca.pem'
      const res = await fetch(url)
      if (!res.ok) throw new Error('Failed to download CA certificate')
      const blob = await res.blob()
      const filename = format === 'pem' ? 'stormrunner-ca.pem' : 'stormrunner-ca.crt'
      downloadBlob(blob, filename)
    } catch (e: any) {
      showFeedback(e.message || 'Failed to download CA certificate', 'error')
    }
  }
INNER

awk -v r="$(cat replacement.txt)" '
  /const handleDownloadCA = async/ { 
    skip=1 
    print r
    next 
  }
  skip && /catch \(e: any\) {/ { skip=0; print; next }
  skip && /^  }/ { if(!done){ skip=0; done=1; next } }
  skip && /^  } catch/ { skip=0; print; next }
  skip { next }
  { print }
' dashboard/src/components/RecordingStudio.tsx > temp.tsx && mv temp.tsx dashboard/src/components/RecordingStudio.tsx

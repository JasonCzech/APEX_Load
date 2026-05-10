import os
import re

with open('src/hooks/useApi.ts', 'r') as f:
    text = f.read()

# I want to just write four files by hand based on the original useApi.ts content to avoid regex nightmares.

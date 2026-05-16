import re

with open("cmd/stormctl/main.go", "r") as f:
    text = f.read()

# Add --api and --format to printUsage and parseRunArgs
# Update runTest to handle API execution

text = text.replace('type runArgs struct {', '''type runArgs struct {
	api      string
	format   string''')

text = text.replace('''func printUsage() {
	fmt.Println(`stormctl - Stormrunner Performance Testing CLI''', '''func printUsage() {
	fmt.Println(`stormctl - Stormrunner Performance Testing CLI''')

# We'll just do it in python block by block

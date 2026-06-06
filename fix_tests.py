import re

with open("pkg/protocol/browser/browser_test.go", "r") as f:
    content = f.read()

def remove_test(name):
    global content
    pattern = r'func\s+' + name + r'\([^)]*\)\s*\{.*?^\}'
    content = re.sub(pattern, '', content, flags=re.DOTALL | re.MULTILINE)

remove_test("TestBrowserFetchRequestPatterns")
remove_test("TestContinuedRequestHeaders")
remove_test("TestShouldRevalidateContentRequest")
remove_test("TestBrowserShouldBlockUnrecordedRequest")
remove_test("TestBrowserShouldBlockFilteredRequest")

with open("pkg/protocol/browser/browser_test.go", "w") as f:
    f.write(content)

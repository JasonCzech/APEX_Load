import re

with open("pkg/protocol/browser/browser.go", "r") as f:
    content = f.read()

# Replace the wrong fields with the right ones
content = content.replace("profile.Engine", "profile.BrowserFamily")
content = content.replace("profile.ScaleFactor", "profile.DeviceScaleFactor")
content = content.replace("profile.Touch", "profile.HasTouch")

with open("pkg/protocol/browser/browser.go", "w") as f:
    f.write(content)

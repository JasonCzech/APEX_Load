# JavaScript twins of the LoadRunner web parity corpus

Each directory holds the VuGen JavaScript form of the `lr_web_parity` case
with the same name: the same steps written with `web.*` and `lr.*` calls in
`Action.js`. `TestJavaScriptTwinsImportLikeTheirCOriginals` imports both and
requires the same DSL (WP4.3). The C case's `default.cfg` is reused.

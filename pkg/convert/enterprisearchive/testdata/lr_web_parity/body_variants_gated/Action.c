Action()
{
	web_custom_request("upload_binary",
		"URL=http://parity.test/api/upload",
		"Method=POST",
		"EncType=application/octet-stream",
		"BodyBinary=\\x00\\x01ABC\\xFF",
		LAST);

	web_custom_request("upload_file",
		"URL=http://parity.test/api/upload",
		"Method=POST",
		"EncType=application/json",
		"BodyFilePath=payload.json",
		LAST);

	web_custom_request("upload_unicode",
		"URL=http://parity.test/api/upload",
		"Method=POST",
		"BodyUnicode=hello",
		LAST);

	return 0;
}

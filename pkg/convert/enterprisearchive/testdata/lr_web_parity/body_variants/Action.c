Action()
{
	lr_save_string("a\\x41", "Token");

	web_custom_request("upload_binary",
		"URL=http://parity.test/api/upload",
		"Method=POST",
		"EncType=application/octet-stream",
		"BodyBinary=\\x00\\x01ABC\\xFF",
		LAST);

	web_custom_request("upload_binary_param",
		"URL=http://parity.test/api/upload",
		"Method=POST",
		"EncType=application/octet-stream",
		"BodyBinary=\\x02{Token}\\x03",
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
		"EncType=text/plain",
		"BodyUnicode=hello {Token}",
		LAST);

	return 0;
}

Action()
{
	web_submit_data("upload",
		"Action=http://parity.test/upload",
		"Method=POST",
		"EncType=multipart/form-data",
		"RecContentType=application/json",
		"Mode=HTTP",
		ITEMDATA,
		"Name=description", "Value=quarterly report", ENDITEM,
		"Name=file", "Value=report.txt", "File=Yes", "ContentType=text/plain", ENDITEM,
		LAST);

	return 0;
}

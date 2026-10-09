Action()
{
	lr_save_string("Q3", "Quarter");

	web_submit_data("upload",
		"Action=http://parity.test/upload",
		"Method=POST",
		"EncType=multipart/form-data",
		"RecContentType=application/json",
		"Mode=HTTP",
		ITEMDATA,
		"Name=description", "Value=quarterly report {Quarter}", ENDITEM,
		"Name=file", "Value=report.txt", "File=Yes", "ContentType=text/plain", ENDITEM,
		"Name=logo", "Value=logo.bin", "File=Yes", "ContentType=application/octet-stream", ENDITEM,
		"Name=summary", "Value=summary.txt", "FilePath=report.txt", "File=Yes", "ContentType=text/plain", ENDITEM,
		LAST);

	return 0;
}

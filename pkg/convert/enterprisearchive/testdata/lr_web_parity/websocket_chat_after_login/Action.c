Action()
{
	web_submit_data("login",
		"Action=http://parity.test/login",
		"Method=POST",
		"RecContentType=text/html",
		"Mode=HTTP",
		ITEMDATA,
		"Name=user", "Value=ada", ENDITEM,
		LAST);

	web_websocket_connect("ID=0",
		"URI=ws://parity.test/chat",
		"Origin=http://parity.test",
		"OnOpenCB=OnOpenCB0",
		"OnMessageCB=OnMessageCB0",
		"OnErrorCB=OnErrorCB0",
		"OnCloseCB=OnCloseCB0",
		LAST);

	web_websocket_send("ID=0",
		"Buffer={\"type\":\"say\",\"text\":\"hello\"}",
		"IsBinary=0",
		LAST);

	web_sync("ParamExists=LastMessage",
		"Timeout=10",
		LAST);

	web_url("history",
		"URL=http://parity.test/history?last={LastMessage}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTTP",
		LAST);

	web_websocket_close("ID=0",
		"Code=1000",
		"Reason=bye",
		LAST);

	return 0;
}

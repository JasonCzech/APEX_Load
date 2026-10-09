void OnOpenCB0 (const char* connectionID, const char * responseHeader, int length)
{
	lr_output_message("WebSocket ID = %s connected sucessfully, first buffer length = %d", connectionID, length);
}

void OnMessageCB0 (const char* connectionID, int isbinary, const char * data, int length)
{
	lr_output_message("WebSocket ID = %s got message=%s length = %d", connectionID, data, length);
	lr_save_string(data, "LastMessage");
}

void OnErrorCB0 (const char* connectionID, const char * message, int length)
{
	lr_output_message("WebSocket ID = %s error occured. Error message = %s", connectionID, message);
}

void OnCloseCB0 (const char* connectionID, int isClosedByClient, int code, const char* reason, int length)
{
	lr_output_message("WebSocket ID = %s closed. close code = %d and reason = %s", connectionID, code, reason);
}

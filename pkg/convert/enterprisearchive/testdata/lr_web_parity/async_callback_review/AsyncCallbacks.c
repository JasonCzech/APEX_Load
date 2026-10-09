int Poll_0_RequestCB()
{
	return WEB_ASYNC_CB_RC_OK;
}

int Poll_0_ResponseBodyBufferCB(
	const char *	aLastBufferStr,
	int				aLastBufferLen,
	const char *	aAccumulatedStr,
	int				aAccumulatedLen,
	int				aHttpStatusCode)
{
	return WEB_ASYNC_CB_RC_OK;
}

int Poll_0_ResponseCB(
	const char *	aResponseHeadersStr,
	int				aResponseHeadersLen,
	const char *	aResponseBodyStr,
	int				aResponseBodyLen,
	int				aHttpStatusCode)
{
	lr_save_string("yes", "PollSeen");

	if (strstr(aResponseBodyStr, "\"done\"") != NULL)
		return WEB_ASYNC_CB_RC_ABORT_ASYNC_NOT_ERROR;

	return WEB_ASYNC_CB_RC_OK;
}

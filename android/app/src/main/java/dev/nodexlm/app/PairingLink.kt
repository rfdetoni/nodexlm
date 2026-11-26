package dev.nodexlm.app

import java.net.URI
import java.net.URLDecoder

/** The QR is untrusted input and never enrolls a node automatically. */
internal fun parsePairingLink(raw: String): Pair<String,String> {
    require(raw.length <= 2048)
    val uri = URI(raw)
    require(uri.scheme == "nodexlm" && uri.host == "pair" && uri.rawUserInfo == null &&
        uri.port == -1 && uri.rawFragment == null && uri.rawPath.isNullOrEmpty())
    val parts = requireNotNull(uri.rawQuery).split('&')
    require(parts.size == 2)
    val params = parts.associate { item ->
        val pair = item.split('=',limit=2)
        require(pair.size == 2)
        pair[0] to URLDecoder.decode(pair[1],"UTF-8")
    }
    require(params.keys == setOf("host","code"))
    val host = requireNotNull(params["host"])
    val code = requireNotNull(params["code"])
    require(code.length == 8 && code.all { it in "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" })
    val endpoint = URI(host)
    require(endpoint.scheme == "https" && !endpoint.host.isNullOrBlank() &&
        endpoint.rawUserInfo == null && endpoint.rawQuery == null && endpoint.rawFragment == null &&
        (endpoint.rawPath.isNullOrEmpty() || endpoint.rawPath == "/"))
    require(endpoint.port == -1 || endpoint.port in 1..65535)
    return host.trimEnd('/') to code
}

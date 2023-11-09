These goroutines show an outbound http/1 RoundTrip request that needs to dial a
new TCP connection and add TLS. It reuses an in-flight DNS request.

package v1

// This file contains a collection of methods that can be used from go-restful to
// generate Swagger API documentation for its models. Please read this PR for more
// information on the implementation: https://github.com/emicklei/go-restful/pull/215
//
// TODOs are ignored from the parser (e.g. TODO(andronat):... || TODO:...) if and only if
// they are on one line! For multiple line or blocks that you want to ignore use ---.
// Any context after a --- is ignored.
//
// Those methods can be generated by using hack/update-swagger-docs.sh

// AUTO-GENERATED FUNCTIONS START HERE
var map_LocalObjectReference = map[string]string{
	"":     "LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace.",
	"name": "name of the referent. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names",
}

func (LocalObjectReference) SwaggerDoc() map[string]string {
	return map_LocalObjectReference
}

var map_Route = map[string]string{
	"":         "A route allows developers to expose services through an HTTP(S) aware load balancing and proxy layer via a public DNS entry. The route may further specify TLS options and a certificate, or specify a public CNAME that the router should also accept for HTTP and HTTPS traffic. An administrator typically configures their router to be visible outside the cluster firewall, and may also add additional security, caching, or traffic controls on the service content. Routers usually talk directly to the service endpoints.\n\nOnce a route is created, the `host` field may not be changed. Generally, routers use the oldest route with a given host when resolving conflicts.\n\nRouters are subject to additional customization and may support additional controls via the annotations field.\n\nBecause administrators may configure multiple routers, the route status field is used to return information to clients about the names and states of the route under each router. If a client chooses a duplicate name, for instance, the route status conditions are used to indicate the route cannot be chosen.\n\nTo enable HTTP/2 ALPN on a route it requires a custom (non-wildcard) certificate. This prevents connection coalescing by clients, notably web browsers. We do not support HTTP/2 ALPN on routes that use the default certificate because of the risk of connection re-use/coalescing. Routes that do not have their own custom certificate will not be HTTP/2 ALPN-enabled on either the frontend or the backend.\n\nCompatibility level 1: Stable within a major release for a minimum of 12 months or 3 minor releases (whichever is longer).",
	"metadata": "metadata is the standard object's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata",
	"spec":     "spec is the desired state of the route",
	"status":   "status is the current state of the route",
}

func (Route) SwaggerDoc() map[string]string {
	return map_Route
}

var map_RouteHTTPHeader = map[string]string{
	"":       "RouteHTTPHeader specifies configuration for setting or deleting an HTTP header.",
	"name":   "name specifies the name of a header on which to perform an action. Its value must be a valid HTTP header name as defined in RFC 2616 section 4.2. The name must consist only of alphanumeric and the following special characters, \"-!#$%&'*+.^_`\". The following header names are reserved and may not be modified via this API: Strict-Transport-Security, Proxy, Cookie, Set-Cookie. It must be no more than 255 characters in length. Header name must be unique.",
	"action": "action specifies actions to perform on headers, such as setting or deleting headers.",
}

func (RouteHTTPHeader) SwaggerDoc() map[string]string {
	return map_RouteHTTPHeader
}

var map_RouteHTTPHeaderActionUnion = map[string]string{
	"":     "RouteHTTPHeaderActionUnion specifies an action to take on an HTTP header.",
	"type": "type defines the type of the action to be applied on the header. Possible values are Set or Delete. Set allows you to set HTTP request and response headers. Delete allows you to delete HTTP request and response headers.",
	"set":  "set defines the HTTP header that should be set: added if it doesn't exist or replaced if it does. This field is required when type is Set and forbidden otherwise.",
}

func (RouteHTTPHeaderActionUnion) SwaggerDoc() map[string]string {
	return map_RouteHTTPHeaderActionUnion
}

var map_RouteHTTPHeaderActions = map[string]string{
	"":         "RouteHTTPHeaderActions defines configuration for actions on HTTP request and response headers.",
	"response": "response is a list of HTTP response headers to modify. Currently, actions may define to either `Set` or `Delete` headers values. Actions defined here will modify the response headers of all requests made through a route. These actions are applied to a specific Route defined within a cluster i.e. connections made through a route. Route actions will be executed before IngressController actions for response headers. Actions are applied in sequence as defined in this list. A maximum of 20 response header actions may be configured. You can use this field to specify HTTP response headers that should be set or deleted when forwarding responses from your application to the client. Sample fetchers allowed are \"res.hdr\" and \"ssl_c_der\". Converters allowed are \"lower\" and \"base64\". Example header values: \"%[res.hdr(X-target),lower]\", \"%{+Q}[ssl_c_der,base64]\". Note: This field cannot be used if your route uses TLS passthrough. ",
	"request":  "request is a list of HTTP request headers to modify. Currently, actions may define to either `Set` or `Delete` headers values. Actions defined here will modify the request headers of all requests made through a route. These actions are applied to a specific Route defined within a cluster i.e. connections made through a route. Currently, actions may define to either `Set` or `Delete` headers values. Route actions will be executed after IngressController actions for request headers. Actions are applied in sequence as defined in this list. A maximum of 20 request header actions may be configured. You can use this field to specify HTTP request headers that should be set or deleted when forwarding connections from the client to your application. Sample fetchers allowed are \"req.hdr\" and \"ssl_c_der\". Converters allowed are \"lower\" and \"base64\". Example header values: \"%[req.hdr(X-target),lower]\", \"%{+Q}[ssl_c_der,base64]\". Any request header configuration applied directly via a Route resource using this API will override header configuration for a header of the same name applied via spec.httpHeaders.actions on the IngressController or route annotation. Note: This field cannot be used if your route uses TLS passthrough. ",
}

func (RouteHTTPHeaderActions) SwaggerDoc() map[string]string {
	return map_RouteHTTPHeaderActions
}

var map_RouteHTTPHeaders = map[string]string{
	"":        "RouteHTTPHeaders defines policy for HTTP headers.",
	"actions": "actions specifies options for modifying headers and their values. Note that this option only applies to cleartext HTTP connections and to secure HTTP connections for which the ingress controller terminates encryption (that is, edge-terminated or reencrypt connections).  Headers cannot be modified for TLS passthrough connections. Setting the HSTS (`Strict-Transport-Security`) header is not supported via actions. `Strict-Transport-Security` may only be configured using the \"haproxy.router.openshift.io/hsts_header\" route annotation, and only in accordance with the policy specified in Ingress.Spec.RequiredHSTSPolicies. In case of HTTP request headers, the actions specified in spec.httpHeaders.actions on the Route will be executed after the actions specified in the IngressController's spec.httpHeaders.actions field. In case of HTTP response headers, the actions specified in spec.httpHeaders.actions on the IngressController will be executed after the actions specified in the Route's spec.httpHeaders.actions field. The headers set via this API will not appear in access logs. Any actions defined here are applied after any actions related to the following other fields: cache-control, spec.clientTLS, spec.httpHeaders.forwardedHeaderPolicy, spec.httpHeaders.uniqueId, and spec.httpHeaders.headerNameCaseAdjustments. The following header names are reserved and may not be modified via this API: Strict-Transport-Security, Proxy, Cookie, Set-Cookie. Note that the total size of all net added headers *after* interpolating dynamic values must not exceed the value of spec.tuningOptions.headerBufferMaxRewriteBytes on the IngressController. Please refer to the documentation for that API field for more details.",
}

func (RouteHTTPHeaders) SwaggerDoc() map[string]string {
	return map_RouteHTTPHeaders
}

var map_RouteIngress = map[string]string{
	"":                        "RouteIngress holds information about the places where a route is exposed.",
	"host":                    "Host is the host string under which the route is exposed; this value is required",
	"routerName":              "Name is a name chosen by the router to identify itself; this value is required",
	"conditions":              "Conditions is the state of the route, may be empty.",
	"wildcardPolicy":          "Wildcard policy is the wildcard policy that was allowed where this route is exposed.",
	"routerCanonicalHostname": "CanonicalHostname is the external host name for the router that can be used as a CNAME for the host requested for this route. This value is optional and may not be set in all cases.",
}

func (RouteIngress) SwaggerDoc() map[string]string {
	return map_RouteIngress
}

var map_RouteIngressCondition = map[string]string{
	"":                   "RouteIngressCondition contains details for the current condition of this route on a particular router.",
	"type":               "Type is the type of the condition. Currently only Admitted or UnservableInFutureVersions.",
	"status":             "Status is the status of the condition. Can be True, False, Unknown.",
	"reason":             "(brief) reason for the condition's last transition, and is usually a machine and human readable constant",
	"message":            "Human readable message indicating details about last transition.",
	"lastTransitionTime": "RFC 3339 date and time when this condition last transitioned",
}

func (RouteIngressCondition) SwaggerDoc() map[string]string {
	return map_RouteIngressCondition
}

var map_RouteList = map[string]string{
	"":         "RouteList is a collection of Routes.\n\nCompatibility level 1: Stable within a major release for a minimum of 12 months or 3 minor releases (whichever is longer).",
	"metadata": "metadata is the standard list's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata",
	"items":    "items is a list of routes",
}

func (RouteList) SwaggerDoc() map[string]string {
	return map_RouteList
}

var map_RoutePort = map[string]string{
	"":           "RoutePort defines a port mapping from a router to an endpoint in the service endpoints.",
	"targetPort": "The target port on pods selected by the service this route points to. If this is a string, it will be looked up as a named port in the target endpoints port list. Required",
}

func (RoutePort) SwaggerDoc() map[string]string {
	return map_RoutePort
}

var map_RouteSetHTTPHeader = map[string]string{
	"":      "RouteSetHTTPHeader specifies what value needs to be set on an HTTP header.",
	"value": "value specifies a header value. Dynamic values can be added. The value will be interpreted as an HAProxy format string as defined in http://cbonte.github.io/haproxy-dconv/2.6/configuration.html#8.2.6 and may use HAProxy's %[] syntax and otherwise must be a valid HTTP header value as defined in https://datatracker.ietf.org/doc/html/rfc7230#section-3.2. The value of this field must be no more than 16384 characters in length. Note that the total size of all net added headers *after* interpolating dynamic values must not exceed the value of spec.tuningOptions.headerBufferMaxRewriteBytes on the IngressController. ",
}

func (RouteSetHTTPHeader) SwaggerDoc() map[string]string {
	return map_RouteSetHTTPHeader
}

var map_RouteSpec = map[string]string{
	"":                  "RouteSpec describes the hostname or path the route exposes, any security information, and one to four backends (services) the route points to. Requests are distributed among the backends depending on the weights assigned to each backend. When using roundrobin scheduling the portion of requests that go to each backend is the backend weight divided by the sum of all of the backend weights. When the backend has more than one endpoint the requests that end up on the backend are roundrobin distributed among the endpoints. Weights are between 0 and 256 with default 100. Weight 0 causes no requests to the backend. If all weights are zero the route will be considered to have no backends and return a standard 503 response.\n\nThe `tls` field is optional and allows specific certificates or behavior for the route. Routers typically configure a default certificate on a wildcard domain to terminate routes without explicit certificates, but custom hostnames usually must choose passthrough (send traffic directly to the backend via the TLS Server-Name- Indication field) or provide a certificate.",
	"host":              "host is an alias/DNS that points to the service. Optional. If not specified a route name will typically be automatically chosen. Must follow DNS952 subdomain conventions.",
	"subdomain":         "subdomain is a DNS subdomain that is requested within the ingress controller's domain (as a subdomain). If host is set this field is ignored. An ingress controller may choose to ignore this suggested name, in which case the controller will report the assigned name in the status.ingress array or refuse to admit the route. If this value is set and the server does not support this field host will be populated automatically. Otherwise host is left empty. The field may have multiple parts separated by a dot, but not all ingress controllers may honor the request. This field may not be changed after creation except by a user with the update routes/custom-host permission.\n\nExample: subdomain `frontend` automatically receives the router subdomain `apps.mycluster.com` to have a full hostname `frontend.apps.mycluster.com`.",
	"path":              "path that the router watches for, to route traffic for to the service. Optional",
	"to":                "to is an object the route should use as the primary backend. Only the Service kind is allowed, and it will be defaulted to Service. If the weight field (0-256 default 100) is set to zero, no traffic will be sent to this backend.",
	"alternateBackends": "alternateBackends allows up to 3 additional backends to be assigned to the route. Only the Service kind is allowed, and it will be defaulted to Service. Use the weight field in RouteTargetReference object to specify relative preference.",
	"port":              "If specified, the port to be used by the router. Most routers will use all endpoints exposed by the service by default - set this value to instruct routers which port to use.",
	"tls":               "The tls field provides the ability to configure certificates and termination for the route.",
	"wildcardPolicy":    "Wildcard policy if any for the route. Currently only 'Subdomain' or 'None' is allowed.",
	"httpHeaders":       "httpHeaders defines policy for HTTP headers.",
}

func (RouteSpec) SwaggerDoc() map[string]string {
	return map_RouteSpec
}

var map_RouteStatus = map[string]string{
	"":        "RouteStatus provides relevant info about the status of a route, including which routers acknowledge it.",
	"ingress": "ingress describes the places where the route may be exposed. The list of ingress points may contain duplicate Host or RouterName values. Routes are considered live once they are `Ready`",
}

func (RouteStatus) SwaggerDoc() map[string]string {
	return map_RouteStatus
}

var map_RouteTargetReference = map[string]string{
	"":       "RouteTargetReference specifies the target that resolve into endpoints. Only the 'Service' kind is allowed. Use 'weight' field to emphasize one over others.",
	"kind":   "The kind of target that the route is referring to. Currently, only 'Service' is allowed",
	"name":   "name of the service/target that is being referred to. e.g. name of the service",
	"weight": "weight as an integer between 0 and 256, default 100, that specifies the target's relative weight against other target reference objects. 0 suppresses requests to this backend.",
}

func (RouteTargetReference) SwaggerDoc() map[string]string {
	return map_RouteTargetReference
}

var map_RouterShard = map[string]string{
	"":          "RouterShard has information of a routing shard and is used to generate host names and routing table entries when a routing shard is allocated for a specific route. Caveat: This is WIP and will likely undergo modifications when sharding support is added.",
	"shardName": "shardName uniquely identifies a router shard in the \"set\" of routers used for routing traffic to the services.",
	"dnsSuffix": "dnsSuffix for the shard ala: shard-1.v3.openshift.com",
}

func (RouterShard) SwaggerDoc() map[string]string {
	return map_RouterShard
}

var map_TLSConfig = map[string]string{
	"":                              "TLSConfig defines config used to secure a route and provide termination",
	"termination":                   "termination indicates termination type.\n\n* edge - TLS termination is done by the router and http is used to communicate with the backend (default) * passthrough - Traffic is sent straight to the destination without the router providing TLS termination * reencrypt - TLS termination is done by the router and https is used to communicate with the backend\n\nNote: passthrough termination is incompatible with httpHeader actions",
	"certificate":                   "certificate provides certificate contents. This should be a single serving certificate, not a certificate chain. Do not include a CA certificate.",
	"key":                           "key provides key file contents",
	"caCertificate":                 "caCertificate provides the cert authority certificate contents",
	"destinationCACertificate":      "destinationCACertificate provides the contents of the ca certificate of the final destination.  When using reencrypt termination this file should be provided in order to have routers use it for health checks on the secure connection. If this field is not specified, the router may provide its own destination CA and perform hostname validation using the short service name (service.namespace.svc), which allows infrastructure generated certificates to automatically verify.",
	"insecureEdgeTerminationPolicy": "insecureEdgeTerminationPolicy indicates the desired behavior for insecure connections to a route. While each router may make its own decisions on which ports to expose, this is normally port 80.\n\nIf a route does not specify insecureEdgeTerminationPolicy, then the default behavior is \"None\".\n\n* Allow - traffic is sent to the server on the insecure port (edge/reencrypt terminations only).\n\n* None - no traffic is allowed on the insecure port (default).\n\n* Redirect - clients are redirected to the secure port.",
	"externalCertificate":           "externalCertificate provides certificate contents as a secret reference. This should be a single serving certificate, not a certificate chain. Do not include a CA certificate. The secret referenced should be present in the same namespace as that of the Route. Forbidden when `certificate` is set.",
}

func (TLSConfig) SwaggerDoc() map[string]string {
	return map_TLSConfig
}

// AUTO-GENERATED FUNCTIONS END HERE

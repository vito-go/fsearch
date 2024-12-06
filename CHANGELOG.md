## 0.0.8
- Optimize grepFromFile, find keywords from the file, use the pipe, support multiple keywords, support regular expression
## 0.0.7

- Add APPName and node info immediately in server when receiving the register request
- Add more comments in the code
- Add server privateIp route to get the private IP of the server. sub path is `/_internal/privateIp`. It is useful when
  the server is behind the NAT.
- WsRegisterSubPath and PrivateIpSubPath are exported in the server package
- Sort the node list by the host name in the server

## 0.0.6

- Use fixed websocket register path `_ws` for the client to register to the server, remove parameter `wsRegister` in the
  NewServer function
- Add parameter `logDir` in the NewClient function, put `appName` in the first place
- fix gin route conflict with the static file, use gin handler

## 0.0.5

- Modify the registry schema with JSON format. So, it does not support the old schema anymore.
- Upgrade go version to 1.20
- Upgrade go mod
- Simplify the code and remove the unused code
- Support html format for the output, also with color,font-size, and href for the traceId link
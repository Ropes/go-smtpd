Pigeon
------

Stupid simple sidecar for intercepting Monit SMTP and adding Host information before querying the OpsGenie API to create an Alert.

Warning: Code could be much cleaner, built in parallel of Brad Fitzpatrick's SMTP example project. Separate data structs are created to handle the introduced complexity.

Called `pigeon` because it relays information, and if it dies you're screwed..

TODO: Output/Logging cleanup
TODO: GCE metadata/Tag full integration

## Basic Workflow  

Using the original forked code pipeline; SMTP `session` interprets incoming lines and constructs an [Basic|OG]Envelope which collects message contents.

Once the Evelope has been built; Close() is called which invokes host information gathering from system calls and logs and writes it to the `Note` field since there is a 130 character limit on the main message.

#Usage
Differing from the normal smtp server:
```
cd pigeon
go build && ./pigeon -ogkey OG_KEY -ogaccnt OG_ACCOUNT
```

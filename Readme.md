

## Todos
- Make reconnect act on signal 
- Handle calling Send without connect - bad idea, context is more straightforward, for some reason empty struct being sent to channel does not lead to interrupt, and sometimes I get panic
- Change mutex to atomic – done 
- ?? Let user cancel context –
- Context for user, channel for internal communication

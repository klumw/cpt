CPT - Copper PostgreSQL Tool
==============================
CPT is a command line tool for monitoring and managing PostgreSQL
based Copper Workflow Engine databases. 
CPT can be used from the command line or within scripts. Access to the PostgreSQL database is configured through an environment variable which makes it easy to run the tool in a containerized environment.  
CPT was primarily developed for the Linux OS and all application examples use the Linux command line syntax.  
More infos about the Copper Workflow Engine can be found at http://www.copper-engine.org  
and https://github.com/klumw/copper-examples.  
 
Installation
------------
Install the GO runtime on your system as described at https://golang.org/doc/install.  
Run ***go build*** from the project root directory.  
On Linux copy the **cpt** binary to **/usr/local/bin**.  
CPT uses the **DATABASE_URL** environment variable to connect to your Copper Postgres database.

**DATABASE_URL Format**
```
postgresql://<user>:<password>@<server>/<database name optional>
```
**DATABASE_URL Example**
```
postgresql://postgres:postgres@localhost
```
The example configuration connects to the PostgreSQL public database on **localhost** with user **postgres** and password **postgres**.  
Export the configured **DATABASE_URL** environment variable to your shell environment before using the CPT command line tool.

CPT Commands
------------
CPT commands are executed directly on the database. A running Copper Engine instance is not needed.
Flags in the form of ***-flag='xxx'*** are used to add a specific configuration or filter.
Execute CPT without any arguments to see the list of possible commands.
For detailed help add ***-help*** after a command (e.g. cpt cleanup -help).

Piping
------
CPT supports command line piping for action commands. With command line piping it is easy to apply various actions on selected data.
If you for example want to delete specific broken workflow instances you can select the broken workflow instances with the ***broken*** command  and then pipe the data to ***cpt delete***.

**Command line piping example**
```
cpt broken -error-time-end=2020-04-26 11:40:40.78 | cpt delete
```
The command chain above will delete all broken workflows that have an error time before 2020-04-26 11:40:40.78 (cpt date time values are in timestamp format)


List of CPT Commands
--------------------

## count
Count workflow instances that are  in a specific state.  

#### -state

The ***-state*** flag applies the state filter.
Possible states are: [ENQUEUED PROCESSING WAITING FINISHED INVALID ERROR ALL]  

**Example**
```
cpt count -state=ERROR
```
Prints amount of workflow instances in ERROR state.

## broken
Print id(s) of broken workflow instance(s).

#### -pattern
Filter on Exception message pattern.

**Example**
```
cpt broken -pattern='OrderCheckWorkflow.java:61"
```
Prints all broken workflow instances with matching pattern  
'OrderCheckWorkflow.java:61' in their exception message. 

#### -error-time-start
Filter on error time of broken workflow instance(s). Only workflow instances with 
an error time > error-time-start are selected.

**Example**
```
cpt broken -error-time-start='2020-04-26 11:40:40.78'
```
Prints all broken workflow instances that have an error time after 2020-04-26 11:40:40.78.

#### -error-time-end
Filter on error time of broken workflow instances. Only workflow instances with 
an error time < error-time-end are selected.

**Example**
```
cpt broken -error-time-end='2020-04-26 11:40:40.78'
```
Prints all broken workflow instances Id's that have an error time before 2020-04-26 11:40:40.78.

#### -class
Filter on workflow class. Workflow class name must contain the full package name.

**Example**
```
cpt broken -workflow-class='org.wkl.copper.full.wf.OrderCheckWorkflow'
```

#### -count
Instead of workflow instance id(s) print amount of selected instances.

## data
Search for workflow instance data by using state and/or a json selector as filters.  
***The json selector is only available for workflow instance data in JSON format.***
Standard Copper Engine uses Java Object Serialization that does not support JSON syntax.
You can use the ***MixedModeSerializer*** at https://github.com/klumw/copper-examples to store instance data in JSON format.

#### -json-selector
JSON data filter that uses a valid PostgreSQL JSON expression.  
Selects workflow instances with matching workflow data selector.

Given the following JSON workflow data example
```
[ "org.wkl.copper.full.data.Order", {

"items" : [ "java.util.ArrayList", [ [ "org.wkl.copper.full.data.Item", {

"quantity" : 2,

"id" : 123,

"description" : "MYK-24242",

"unitPrice" : 4.65

} ] ] ],

"customerId" : 3303799,

"credit_card" : "7277226662630000",

"accountId" : "60003763",

"orderId" : 3766,

"state" : "CreditCardCheck"

} ]
```

and the JSON selector
```
cpt data -selector=json->1->>'state' = 'CreditCardCheck'
```
The selector matches the state field with ***state='CreditCardCheck'***.
The root element for all JSON selectors is always ***json***.

A full description of all available PostgreSQL JSON operators
can be found at https://www.postgresql.org/docs/current/functions-json.html

#### -state
Filter on workflow instance state.

**Example**
```
cpt data -state=ERROR  -selector=json->1->>'state' = 'CreditCardCheck'
```
Selects workflow instances with workflow ***state=ERROR*** and instance data containing the field **state** with a value of ***'CreditCardCheck'***.

## cleanup
Cleanup command for the Copper database. Cleanup removes outdated data from the Copper Engine database.  
All tables are scanned and outdated entries are deleted. 

#### -age
Selects all data older than age.

Possible age formats are:  

**Hours**
```
cpt cleanup -age=24h
```
Removes all workflow data older than 24 hours.

**Days**
```
cpt cleanup -age=30d
```
Removes all data older than 30 days.

**Timestamp**
```
cpt cleanup -age='2020-04-25 11:40:40.78'
```
Removes all workflow data older than 2020-04-25 11:40:40.78.

### -audit-trail
Removes audit trail data.

### -workflow-instance
Removes instance data.

## show
Print workflow instance information for given workflow instance ids.  
Show supports command line piping.

#### -workflow-data
Print workflow instance data. Only useful for workflow instance data in clear text (e.g. JSON) format.

#### -instance-details
Print workflow instance details.

**Example**
```
cpt show -instance-details b4d9dd80-c731-414c-871e-28df65dbfc41
```
Prints workflow details for instance id b4d9dd80-c731-414c-871e-28df65dbfc41.

#### -audit-trail
Print audit trail message(s).

**Example**
```
cpt show -audit-trail b4d9dd80-c731-414c-871e-28df65dbfc41
```
Prints all audit-trail messages for instance id b4d9dd80-c731-414c-871e-28df65dbfc41.

#### -print-data-array
Prints all workflow instance data in JSON array format.  
Only valid together with the ***-workflow-data*** flag.  
All instance data must be of type JSON.

## delete
Delete workflow instance(s).  
Supports command line piping.

**Example**
```
cpt delete b4d9dd80-c731-414c-871e-28df65dbfc41 b4d9dd80-c731-414c-871e-28df65dbfc22
```
Deletes the given workflow instances from the database.

## restart
Restart broken workflow instance(s).  
Supports command line piping.

**Example**
```
cpt restart b4d9dd80-c731-414c-871e-28df65dbfc41 b4d9dd80-c731-414c-871e-28df65dbfc22
```
Restarts given broken workflows.
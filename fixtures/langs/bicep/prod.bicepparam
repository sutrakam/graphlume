using './main.bicep'

var environment = 'prod'

param prefix = 'contoso-${environment}'
param skuName = 'Standard_GRS'
param tags = {
  owner: 'platform'
  env: environment
}

metadata description = 'Storage account with a diagnostics workspace.'

targetScope = 'resourceGroup'

@description('Name prefix for every resource.')
@minLength(3)
param prefix string

param location string = resourceGroup().location

@allowed([
  'Standard_LRS'
  'Standard_GRS'
])
param skuName string = 'Standard_LRS'

param tags object = {}

type storageSku = 'Standard_LRS' | 'Standard_GRS'

@export()
type networkRule = {
  name: string
  cidr: string
}

var storageName = '${prefix}${uniqueString(resourceGroup().id)}'
var rules = [
  {
    name: 'office'
    cidr: '10.0.0.0/24'
  }
]

func buildName(base string, suffix string) string => '${base}-${suffix}'

resource workspace 'Microsoft.OperationalInsights/workspaces@2022-10-01' = {
  name: '${prefix}-law'
  location: location
  properties: {
    sku: {
      name: 'PerGB2018'
    }
  }
}

resource storage 'Microsoft.Storage/storageAccounts@2023-01-01' = {
  name: storageName
  location: location
  sku: {
    name: skuName
  }
  kind: 'StorageV2'
  tags: tags
}

resource blobService 'Microsoft.Storage/storageAccounts/blobServices@2023-01-01' = {
  parent: storage
  name: 'default'
}

resource existingVault 'Microsoft.KeyVault/vaults@2023-02-01' existing = {
  name: '${prefix}-kv'
}

resource containers 'Microsoft.Storage/storageAccounts/blobServices/containers@2023-01-01' = [for rule in rules: {
  parent: blobService
  name: rule.name
}]

module network 'modules/network.bicep' = {
  name: 'networkDeploy'
  params: {
    location: location
    prefix: prefix
  }
}

module registry 'br/public:avm/res/container-registry/registry:0.1.1' = if (!empty(prefix)) {
  name: 'registryDeploy'
  params: {
    name: buildName(prefix, 'acr')
  }
}

output storageId string = storage.id
output blobEndpoint string = storage.properties.primaryEndpoints.blob
output vaultUri string = existingVault.properties.vaultUri

var IPV4_FUNCS = {};
var IPV4_CONST = {
	bitLength:32,
	bitsPerByte:8,
	cidrRE:/(?:\d{1,3}[.]){3}\d{1,3}[/]\d{1,2}/,
	ipv4RE:/(?:\d{1,3}[.]){3}\d{1,3}/,
	ipv4NetMaskRE:/(?:\d{1,3}[.]){3}\d{1,3} (?:\d{1,3}[.]){3}\d{1,3}/	
}

IPV4_FUNCS.getClassfulNetwork=function(network)
{
	if(IPV4_CONST.cidrRE.test(network))
	{
		return network;		
	}
	
	var addressParts = network.split(".");
	var zeroCount = addressParts.filter(function(x){return parseInt(x) === 0}).length;
	return network + "/" + (zeroCount * 8);
	
	
}

IPV4_FUNCS.isPrivate=function(address)
{
	if(!IPV4_CONST.ipv4RE.test(address))
	{
		return false;
	}
	
	var addressParts = address.split(".");
	
	if(parseInt(addressParts[0]) == 10)
	{
		return true;
	}
	else if(parseInt(addressParts[0]) == 172)
	{
		if(parseInt(addressParts[1]) >= 16 && parseInt(addressParts[1]) <= 31)
		{
			return true;
		}
		
	}
	else if(parseInt(addressParts[0]) == 192 && parseInt(addressParts[1]) == 168)
	{
		return true;
		
	}
	
	return false;
	
}

IPV4_FUNCS.isLoopback=function(address)
{
	if(!IPV4_CONST.ipv4RE.test(address))
	{
		return false;
	}
	
	var addressParts = address.split(".");
	
	return parseInt(addressParts[0]) == 127 ? true : false;
		
}

IPV4_FUNCS.addressToBinary=function(address)
{
	if(!IPV4_CONST.ipv4RE.test(address))
	{
		return;
	}
	var addressParts = [];
	var octets = address.split(".")
	var i;
	var binAddress;
	var padding;
	
	for(i=0;i<octets.length;i++)
	{
		binAddress = parseInt(octets[i]).toString(2);
		padding = "0".repeat(IPV4_CONST.bitsPerByte - binAddress.length);
		addressParts.push(padding + binAddress);
		
	}
	
	return addressParts.join("");	
	
}

IPV4_FUNCS.binaryToAddress=function(binaryAddress)
{
	
	if(binaryAddress.length != IPV4_CONST.bitLength)
	{
		return;
	}
	
	var addressParts = [];
	var i;
	
	
	for(i=0;i<binaryAddress.length;i=i+IPV4_CONST.bitsPerByte)
	{
		////window.console.log(i,i+IPV4_CONST.bitsPerByte-1);
		////window.console.log(binaryAddress.substring(i,i+IPV4_CONST.bitsPerByte-1));
		addressParts.push(parseInt(binaryAddress.substr(i,IPV4_CONST.bitsPerByte),2));
		
	}	
	
	return addressParts.join(".");
	
}

IPV4_FUNCS.invertAddress=function(address)
{

	var invertedAddress = [];
	var addressParts = address.split(".");
	var i;
	
	for(i=0;i<addressParts.length;i++)
	{
		invertedAddress.push(255 - parseInt(addressParts[i]));
	}
	
	return invertedAddress.join(".");

}

function IPV4(address)
{
	this.setAddress(address);
}
IPV4.prototype.setAddress=function(address)
{
	var addressParts;
	
	if(IPV4_CONST.cidrRE.test(address))
	{
		addressParts = address.split("/");
		this.address = addressParts[0];
		
		var strCIDR = addressParts[addressParts.length - 1];
		
		this.cidr = parseInt(strCIDR) >= 1 && parseInt(strCIDR) <= IPV4_CONST.bitLength ? parseInt(strCIDR) : undefined;
		this.netmask = this.maskFromCIDR(this.cidr);
		this.broadcastAddress = this.broadcast();
		this.networkAddress = this.network();
		
	}
	else if(IPV4_CONST.ipv4NetMaskRE.test(address))
	{
		addressParts = address.split(" ");
		this.address = addressParts[0];
		this.netmask = addressParts[addressParts.length -1];
		this.cidr = this.cidrFromMask(this.netmask);
		this.broadcastAddress = this.broadcast();
		this.networkAddress = this.network();
		
	}
}

IPV4.prototype.shortNotation=function()
{
	if(this.cidr === undefined)
	{
			return;
	}
	
	return this.address + "/" + this.cidr
	
}

IPV4.prototype.broadcast=function()
{
	if(this.cidr === undefined)
	{
			return;
	}
		
	var addressBinary = IPV4_FUNCS.addressToBinary(this.address);
	var broadcastAddress = addressBinary.slice(0,this.cidr) + "1".repeat(IPV4_CONST.bitLength - this.cidr);
	return IPV4_FUNCS.binaryToAddress(broadcastAddress);

}

IPV4.prototype.network=function()
{
	if(this.cidr === undefined)
	{
			return;
	}
		
	var addressBinary = IPV4_FUNCS.addressToBinary(this.address);
	var broadcastAddress = addressBinary.slice(0,this.cidr) + "0".repeat(IPV4_CONST.bitLength - this.cidr);
	return IPV4_FUNCS.binaryToAddress(broadcastAddress);

}

IPV4.prototype.range=function()
{
	
	if(this.networkAddress !== undefined && this.broadcastAddress !== undefined)
	{
		return this.networkAddress + " - " + this.broadcastAddress;
	}	
	
}

IPV4.prototype.cidrFromMask=function(netmask)
{
	
	var netmaskBinary = IPV4_FUNCS.addressToBinary(netmask);
	var count = netmaskBinary.match(/[1]/g);
	return count !== null ? count.length : 0;
	
}

IPV4.prototype.maskFromCIDR=function(cidr)
{
	if(this.cidr === undefined)
	{
		return;
	}
	
	if(this.cidr >=1 && this.cidr <= IPV4_CONST.bitLength)
	{
		var netmask = "1".repeat(cidr) + "0".repeat(IPV4_CONST.bitLength - cidr)
		return IPV4_FUNCS.binaryToAddress(netmask);
	}
	
}



IPV4.prototype.wildcardMask=function()
{

	if(this.netmask === undefined)
	{
		return;		
	}
	
	if(!IPV4_CONST.ipv4RE.test(this.netmask))
	{
		return;		
	}
	
	return IPV4_FUNCS.invertAddress(this.netmask);
		
}



IPV4.prototype.contains=function(network)
{
	if(!IPV4_CONST.ipv4RE.test(network))
	{
		return false;
	}
	
	var ipNetwork = new IPV4(network)
	
	if(ipNetwork.address === undefined)
	{
		ipNetwork = new IPV4(network + "/32");
		
	}
		
	return (this.inNetwork(ipNetwork.address) && this.inNetwork(ipNetwork.broadcastAddress))	         
	
}

IPV4.prototype.inNetwork=function(address)
{
	if(!IPV4_CONST.ipv4RE.test(address))
	{
		return false;
	}
	
	var networkBinary = IPV4_FUNCS.addressToBinary(this.networkAddress);	
	var broadcastBinary = IPV4_FUNCS.addressToBinary(this.broadcastAddress);	
	var addressBinary = IPV4_FUNCS.addressToBinary(address)
	
	
	////window.console.log(parseInt(networkBinary,2) + ":" + parseInt(addressBinary,2) + ":" + parseInt(broadcastBinary,2));
	return (parseInt(networkBinary,2) <= parseInt(addressBinary,2)) &&  (parseInt(addressBinary,2)<= parseInt(broadcastBinary,2));
	
	
}




const get = async (url: string) => (await fetch(url)).text();
